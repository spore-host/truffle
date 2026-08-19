package aws

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/spore-host/truffle/pkg/testutil"
)

// regionFailingTransport fails every request whose SigV4 Authorization header
// names failRegion, and passes everything else through to base. SigV4 embeds
// the target region as "/<region>/<service>/aws4_request" in the credential
// scope, so this reliably targets one region's calls in a multi-region fan-out
// without needing per-region client instances.
type regionFailingTransport struct {
	base       http.RoundTripper
	failRegion string
}

func (t *regionFailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.Header.Get("Authorization"), "/"+t.failRegion+"/ec2/") {
		return nil, errors.New("simulated network failure for " + t.failRegion)
	}
	return t.base.RoundTrip(req)
}

// newPartialFailureClient returns a Client backed by a real Substrate emulator
// where every call scoped to failRegion errors and every other region's calls
// succeed normally — a controlled partial failure, as opposed to
// newUnreachableClient's total failure. WithRetryMaxAttempts(1) keeps the
// simulated failure from being retried three times per call.
func newPartialFailureClient(t *testing.T, failRegion string) *Client {
	t.Helper()
	env := testutil.SubstrateServer(t)

	cfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithBaseEndpoint(env.URL),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "test"),
		),
		awsconfig.WithHTTPClient(&http.Client{
			Transport: &regionFailingTransport{base: http.DefaultTransport, failRegion: failRegion},
		}),
		awsconfig.WithRetryMaxAttempts(1),
	)
	if err != nil {
		t.Fatalf("build AWS config: %v", err)
	}
	return NewClientFromConfig(cfg)
}

// TestSearchInstanceTypesByRegion_PartialFailure is the regression test for
// #117: a caller embedding truffle as a library cannot observe which region
// failed in a multi-region SearchInstanceTypes call — only a stderr warning
// says so, which an in-process consumer has no reasonable way to capture and
// attribute. SearchInstanceTypesByRegion must return a RegionResult per region
// so the failure is data, not print output.
func TestSearchInstanceTypesByRegion_PartialFailure(t *testing.T) {
	c := newPartialFailureClient(t, "us-west-2")
	matcher := regexp.MustCompile(`^t3\.micro$`)

	results, regionResults, err := c.SearchInstanceTypesByRegion(context.Background(),
		[]string{"us-east-1", "us-west-2"}, matcher, FilterOptions{})

	// A partial failure must not be promoted to a hard error — the #63 contract
	// only fires when every region fails.
	if err != nil {
		t.Fatalf("partial failure returned an error, want nil (only all-regions-failed does that): %v", err)
	}

	// us-east-1 succeeded and must be present in the flat results, same as today.
	found := false
	for _, r := range results {
		if r.Region == "us-east-1" {
			found = true
		}
		if r.Region == "us-west-2" {
			t.Errorf("us-west-2 was the failing region but still contributed a result: %+v", r)
		}
	}
	if !found {
		t.Error("us-east-1 (the succeeding region) is missing from results")
	}

	// The whole point: regionResults must attribute the failure to us-west-2
	// specifically, and confirm us-east-1 as a clean success — not just "some
	// region somewhere failed".
	if len(regionResults) != 2 {
		t.Fatalf("regionResults has %d entries, want 2 (one per queried region)", len(regionResults))
	}
	var east, west *RegionResult
	for i := range regionResults {
		switch regionResults[i].Region {
		case "us-east-1":
			east = &regionResults[i]
		case "us-west-2":
			west = &regionResults[i]
		}
	}
	if east == nil || west == nil {
		t.Fatalf("regionResults missing an expected region: %+v", regionResults)
	}
	if east.Err != nil {
		t.Errorf("us-east-1.Err = %v, want nil (it succeeded)", east.Err)
	}
	if east.Count != 1 {
		t.Errorf("us-east-1.Count = %d, want 1 (t3.micro)", east.Count)
	}
	if west.Err == nil {
		t.Error("us-west-2.Err = nil, want the simulated failure — this is the attribution #117 asks for")
	}
	if west.Count != 0 {
		t.Errorf("us-west-2.Count = %d, want 0 (the query never completed)", west.Count)
	}
}

// TestSearchInstanceTypesByRegion_AllSucceed pins the ordinary case: with no
// failures, every RegionResult.Err is nil and Count matches what was returned,
// so the new return value doesn't change behavior when nothing goes wrong.
func TestSearchInstanceTypesByRegion_AllSucceed(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	matcher := regexp.MustCompile(`^t3\.micro$`)

	results, regionResults, err := c.SearchInstanceTypesByRegion(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchInstanceTypesByRegion() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for t3.micro in us-east-1")
	}
	if len(regionResults) != 1 || regionResults[0].Err != nil {
		t.Errorf("regionResults = %+v, want one entry with Err == nil", regionResults)
	}
	if regionResults[0].Count != len(results) {
		t.Errorf("regionResults[0].Count = %d, want %d (len(results))", regionResults[0].Count, len(results))
	}
}

// TestSearchInstanceTypesByRegion_AllFail confirms the sibling method honors
// the same #63 contract as SearchInstanceTypes: when every region fails, the
// error return is non-nil rather than an empty success — this is the case
// SearchInstanceTypes delegates to it for.
func TestSearchInstanceTypesByRegion_AllFail(t *testing.T) {
	c := newUnreachableClient(t)
	matcher := regexp.MustCompile(`^m7i\.large$`)

	results, regionResults, err := c.SearchInstanceTypesByRegion(context.Background(),
		[]string{"us-east-1", "us-west-2"}, matcher, FilterOptions{})

	if err == nil {
		t.Fatalf("expected an error when all regions fail, got nil (results=%d)", len(results))
	}
	if !strings.Contains(err.Error(), "region queries failed") {
		t.Errorf("error should explain the total failure, got: %v", err)
	}
	if len(regionResults) != 2 {
		t.Fatalf("regionResults has %d entries, want 2", len(regionResults))
	}
	for _, rr := range regionResults {
		if rr.Err == nil {
			t.Errorf("region %s: Err = nil, want a failure (every region failed)", rr.Region)
		}
	}
}

// TestSearchInstanceTypes_DelegatesRegionAttribution verifies the original
// SearchInstanceTypes signature is preserved (still returns just
// ([]InstanceTypeResult, error)) and its behavior is unchanged for the caller
// that doesn't need per-region attribution — it's a thin wrapper now, not a
// second implementation that could drift from SearchInstanceTypesByRegion.
func TestSearchInstanceTypes_DelegatesRegionAttribution(t *testing.T) {
	c := newPartialFailureClient(t, "us-west-2")
	matcher := regexp.MustCompile(`^t3\.micro$`)

	results, err := c.SearchInstanceTypes(context.Background(),
		[]string{"us-east-1", "us-west-2"}, matcher, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchInstanceTypes() error = %v, want nil (partial failure, not total)", err)
	}
	if len(results) != 1 || results[0].Region != "us-east-1" {
		t.Errorf("results = %+v, want exactly the us-east-1 result", results)
	}
}

package aws

import (
	"context"
	"regexp"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// newUnreachableClient returns a Client whose AWS calls all fail (the base
// endpoint points at a closed port), so every per-region query errors.
func newUnreachableClient(t *testing.T) *Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion("us-east-1"),
		// 127.0.0.1:1 — nothing listens here, so the SDK call fails fast.
		awsconfig.WithBaseEndpoint("http://127.0.0.1:1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "test"),
		),
	)
	if err != nil {
		t.Fatalf("build AWS config: %v", err)
	}
	return NewClientFromConfig(cfg)
}

// TestSearchInstanceTypes_AllRegionsFail verifies that when every region query
// errors, SearchInstanceTypes returns an error rather than (empty, nil) — a
// total failure must not masquerade as a legitimate "no matches" result (#63).
func TestSearchInstanceTypes_AllRegionsFail(t *testing.T) {
	c := newUnreachableClient(t)
	matcher := regexp.MustCompile(`^m7i\.large$`)

	results, err := c.SearchInstanceTypes(context.Background(),
		[]string{"us-east-1", "us-west-2"}, matcher, FilterOptions{})

	if err == nil {
		t.Fatalf("expected an error when all regions fail, got nil (results=%d)", len(results))
	}
	if !strings.Contains(err.Error(), "region queries failed") {
		t.Errorf("error should explain the total failure, got: %v", err)
	}
}

// TestGetSpotPricing_AllRegionsFail verifies the same contract for spot pricing.
func TestGetSpotPricing_AllRegionsFail(t *testing.T) {
	c := newUnreachableClient(t)
	instances := []InstanceTypeResult{
		{InstanceType: "m7i.large", Region: "us-east-1"},
		{InstanceType: "m7i.large", Region: "us-west-2"},
	}

	results, err := c.GetSpotPricing(context.Background(), instances, SpotOptions{LookbackHours: 1})

	if err == nil {
		t.Fatalf("expected an error when all regions fail, got nil (results=%d)", len(results))
	}
	if !strings.Contains(err.Error(), "region Spot price queries failed") {
		t.Errorf("error should explain the total failure, got: %v", err)
	}
}

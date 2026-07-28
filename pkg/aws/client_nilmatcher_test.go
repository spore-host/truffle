package aws

import (
	"context"
	"regexp"
	"testing"

	"github.com/spore-host/truffle/pkg/testutil"
)

// The nil-matcher contract (#106): a nil *regexp.Regexp means "no instance-type
// constraint", never a panic.
//
// This matters more than a typical input-validation case because the search fans
// out into per-region goroutines. A panic there cannot be recovered by the
// caller — recover() only works in the panicking goroutine — so for an in-process
// embedder like spore-host-mcp (a single stdio server process) one bad argument
// would take down the whole process rather than failing one call.
//
// The tests below cover the entry points rather than only the helper, because
// there are three separate dereference sites across two exported functions:
// extractSpecificTypes (client.go), the per-type match in searchInRegion
// (client.go), and the offered-type match in searchSageMakerInRegion
// (sagemaker.go). Guarding only the first would relocate the panic, not remove it.

// TestExtractSpecificTypes_NilMatcher covers the originally-reported site: a nil
// matcher yields no API-side type filter, so the caller fetches all types.
func TestExtractSpecificTypes_NilMatcher(t *testing.T) {
	if got := extractSpecificTypes(nil); got != nil {
		t.Errorf("extractSpecificTypes(nil) = %v, want nil (no API filter → fetch all)", got)
	}
}

// TestExtractSpecificTypes_Patterns pins the surrounding behaviour so the nil
// guard can't be confused with the wildcard path: both return nil, but an exact
// pattern must still produce an API-side filter.
func TestExtractSpecificTypes_Patterns(t *testing.T) {
	if got := extractSpecificTypes(regexp.MustCompile(`.*`)); got != nil {
		t.Errorf("wildcard: got %v, want nil (fetch all)", got)
	}
	got := extractSpecificTypes(regexp.MustCompile(`^t3\.micro$`))
	if len(got) != 1 || string(got[0]) != "t3.micro" {
		t.Errorf("exact pattern: got %v, want [t3.micro]", got)
	}
}

// TestSearchInstanceTypes_NilMatcher is the regression test that actually pins
// the bug: it drives the full exported call with a nil matcher, so it fails (by
// crashing the test binary) if any dereference site in the goroutine path is
// left unguarded. A nil matcher must behave like ".*".
func TestSearchInstanceTypes_NilMatcher(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()

	c := NewClientFromConfig(env.AWSConfig)

	nilResults, err := c.SearchInstanceTypes(ctx, []string{"us-east-1"}, nil, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchInstanceTypes(nil matcher) error = %v", err)
	}
	if len(nilResults) == 0 {
		t.Fatal("SearchInstanceTypes(nil matcher) returned 0 results, want the full catalog")
	}

	// "No constraint" must mean the same thing as a match-everything pattern.
	starResults, err := c.SearchInstanceTypes(ctx, []string{"us-east-1"}, regexp.MustCompile(".*"), FilterOptions{})
	if err != nil {
		t.Fatalf("SearchInstanceTypes(.*) error = %v", err)
	}
	if len(nilResults) != len(starResults) {
		t.Errorf("nil matcher returned %d results, .* returned %d — nil must mean no constraint",
			len(nilResults), len(starResults))
	}
}

// TestSearchInstanceTypes_NilMatcherRespectsFilters verifies the nil guard only
// drops the *type* constraint: the other FilterOptions still apply, so a nil
// matcher isn't a way to bypass filtering.
func TestSearchInstanceTypes_NilMatcherRespectsFilters(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()

	c := NewClientFromConfig(env.AWSConfig)

	results, err := c.SearchInstanceTypes(ctx, []string{"us-east-1"}, nil, FilterOptions{MinVCPUs: 4})
	if err != nil {
		t.Fatalf("SearchInstanceTypes() error = %v", err)
	}
	for _, r := range results {
		if r.VCPUs < 4 {
			t.Errorf("result %s has %d vCPUs, want >= 4 (filters still apply with a nil matcher)",
				r.InstanceType, r.VCPUs)
		}
	}
}

// TestSearchSageMakerInstanceTypes_NilMatcher covers the mirror-image site in the
// SageMaker search path, which has the same goroutine fan-out and so the same
// unrecoverable-panic exposure.
func TestSearchSageMakerInstanceTypes_NilMatcher(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()

	c := NewClientFromConfig(env.AWSConfig)

	// Must not panic. An error is acceptable here (Substrate may not implement the
	// Service Quotas surface the SageMaker path needs); a crash is not.
	if _, err := c.SearchSageMakerInstanceTypes(ctx, []string{"us-east-1"}, nil, FilterOptions{}); err != nil {
		t.Logf("SearchSageMakerInstanceTypes(nil matcher) returned error (acceptable): %v", err)
	}
}

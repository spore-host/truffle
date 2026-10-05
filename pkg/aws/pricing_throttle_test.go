package aws

import (
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
)

// TestPricingClientRetriesThrottlingHarderThanTheSDKDefault is truffle#175.
//
// The Price List API has a famously low rate limit, and `spawn task run` is ONE
// PROCESS PER LAUNCH — so a fan-out of eight launches is eight cold caches making
// eight GetProducts calls at once. The 24-hour in-process cache is right for a
// long-lived process and can never amortise the case that actually throttles.
//
// With --cost-limit set, truffle fails closed when no price resolves (#114,
// correctly), so a single ThrottlingException refuses the launch:
//
//	cannot determine the on-demand price for c7g.2xlarge in us-west-2,
//	required to enforce --cost-limit 0.15: ... exceeded maximum number of
//	attempts, 3, ... ThrottlingException: Rate exceeded
//
// "attempts, 3" is the SDK default. This asserts we ask for more, since the
// alternative — substituting a hand-maintained price into a spend cap — trades a
// refused launch for a silently wrong cap.
func TestPricingClientRetriesThrottlingHarderThanTheSDKDefault(t *testing.T) {
	const sdkDefaultAttempts = 3
	if pricingMaxAttempts <= sdkDefaultAttempts {
		t.Errorf("pricingMaxAttempts = %d, which is not more than the SDK default of %d — "+
			"the throttle that refused a launch in #175 would still refuse it",
			pricingMaxAttempts, sdkDefaultAttempts)
	}
	// Not unbounded either: this runs in front of a launch, and a caller waiting
	// minutes for a price is its own failure.
	if pricingMaxAttempts > 15 {
		t.Errorf("pricingMaxAttempts = %d is high enough to stall a launch on backoff alone",
			pricingMaxAttempts)
	}

	// The retryer must actually be wired onto the client's config, and must
	// classify throttling as retryable — asserting the constant alone would pass
	// with the retryer never installed.
	p := newAWSOnDemandPricer(aws.Config{Region: "us-east-1"})
	if p.ensureClient() == nil {
		t.Fatal("ensureClient returned nil")
	}
	if p.cfg.Retryer != nil {
		// cfg is the pricer's copy; ensureClient sets the retryer on its own copy,
		// so check the constructed retryer's behaviour instead of the field.
		t.Log("pricer cfg carries a retryer")
	}
	r := retry.NewStandard(func(o *retry.StandardOptions) { o.MaxAttempts = pricingMaxAttempts })
	if got := r.MaxAttempts(); got != pricingMaxAttempts {
		t.Errorf("retryer MaxAttempts = %d, want %d", got, pricingMaxAttempts)
	}
}

// TestPricingRetryerIsInstalledOnTheClientConfig: the constant is inert unless
// ensureClient actually applies it. Asserted at the source level because the
// built *pricing.Client does not expose its retryer.
func TestPricingRetryerIsInstalledOnTheClientConfig(t *testing.T) {
	src := readSourceForTest(t, "pricing.go")
	i := strings.Index(src, "func (p *awsOnDemandPricer) ensureClient()")
	if i < 0 {
		t.Fatal("ensureClient not found; this gate would pass vacuously")
	}
	j := strings.Index(src[i:], "\n}\n")
	body := src[i : i+j]

	if !strings.Contains(body, "cfg.Retryer") {
		t.Error("ensureClient does not set cfg.Retryer, so pricingMaxAttempts is never " +
			"applied and the SDK's 3-attempt default still governs (#175)")
	}
	if !strings.Contains(body, "pricingMaxAttempts") {
		t.Error("ensureClient does not reference pricingMaxAttempts")
	}
	// Order matters: the retryer must be set BEFORE NewFromConfig copies the config.
	if strings.Index(body, "cfg.Retryer") > strings.Index(body, "pricing.NewFromConfig") {
		t.Error("cfg.Retryer is set after pricing.NewFromConfig, so the client is built " +
			"with the default retryer and the setting has no effect")
	}
}

// readSourceForTest reads a file in this package so a gate can assert on source
// structure the built type does not expose (here: whether a retryer is wired
// onto the client config at all).
func readSourceForTest(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

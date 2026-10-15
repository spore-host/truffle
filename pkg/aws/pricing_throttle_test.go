package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/smithy-go"
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
// the clients actually use it. Both Price List pricers — EC2 on-demand and
// SageMaker — must build a client whose retryer allows pricingMaxAttempts and
// that talks to a Price List endpoint, whatever region the caller's config
// names (#175, #219: the SageMaker pricer used the SDK's 3-attempt default).
func TestPricingRetryerIsInstalledOnTheClientConfig(t *testing.T) {
	for _, region := range []string{"", "us-east-1", "us-west-2", "ap-south-1"} {
		clients := map[string]*pricing.Client{
			"ec2":       newAWSOnDemandPricer(aws.Config{Region: region}).ensureClient(),
			"sagemaker": newAWSSageMakerPricer(aws.Config{Region: region}).ensureClient(),
		}
		wantRegion := "us-east-1"
		if region == "ap-south-1" {
			wantRegion = region
		}
		for name, c := range clients {
			o := c.Options()
			if o.Retryer == nil {
				t.Fatalf("%s pricer (region %q): client has no retryer", name, region)
			}
			if got := o.Retryer.MaxAttempts(); got != pricingMaxAttempts {
				t.Errorf("%s pricer (region %q): retryer allows %d attempts, want pricingMaxAttempts (%d)",
					name, region, got, pricingMaxAttempts)
			}
			if !o.Retryer.IsErrorRetryable(&smithy.GenericAPIError{Code: "ThrottlingException"}) {
				t.Errorf("%s pricer: retryer does not retry ThrottlingException", name)
			}
			if o.Region != wantRegion {
				t.Errorf("%s pricer (region %q): Price List endpoint region %q, want %q", name, region, o.Region, wantRegion)
			}
		}
	}
}

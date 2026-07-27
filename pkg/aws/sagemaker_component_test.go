package aws

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// Component-preference tests for #107. The live rates below were measured
// 2026-07-27 in us-east-1 for ml.p4d.24xlarge, the type that broke the old
// "all compute components match to the cent" assumption:
//
//	Cluster (HyperPod) $25.910   Hosting/Training/... $25.251   (2.6% spread)
const (
	p4dClusterRate = "25.9100000000"
	p4dHostingRate = "25.2510000000"
)

// smItemNamed builds a price document for an arbitrary instance type, so these
// tests can use the real ml.p4d.24xlarge rates rather than smItem's ml.c5d.xlarge.
func smItemNamed(instanceType, component, usd string) string {
	return `{
		"product": {"attributes": {
			"instanceName": "` + instanceType + `",
			"regionCode": "us-east-1",
			"component": "` + component + `"
		}},
		"terms": {"OnDemand": {"A": {"priceDimensions": {"B": {
			"unit": "Hrs", "pricePerUnit": {"USD": "` + usd + `"}
		}}}}}
	}`
}

// TestPickSageMakerRate_PrefersHostingOverCluster is the core #107 regression: a
// caller pricing an inference endpoint must not receive the HyperPod (Cluster)
// rate just because AWS happened to return that row first.
func TestPickSageMakerRate_PrefersHostingOverCluster(t *testing.T) {
	// Cluster first — the ordering that produced the wrong answer before the fix.
	list := []string{
		smItemNamed("ml.p4d.24xlarge", "Cluster", p4dClusterRate),
		smItemNamed("ml.p4d.24xlarge", "Hosting", p4dHostingRate),
		smItemNamed("ml.p4d.24xlarge", "Training", p4dHostingRate),
	}
	price, err := pickSageMakerRate(list)
	if err != nil {
		t.Fatalf("pickSageMakerRate error: %v", err)
	}
	if math.Abs(price-25.251) > 1e-9 {
		t.Errorf("price = %v, want 25.251 (Hosting); Cluster/HyperPod at 25.910 must not win", price)
	}
}

// TestPickSageMakerRate_OrderIndependent verifies the selected rate no longer
// depends on Price List response ordering — the property the old first-match
// implementation lacked. Every permutation must agree.
func TestPickSageMakerRate_OrderIndependent(t *testing.T) {
	cluster := smItemNamed("ml.p4d.24xlarge", "Cluster", p4dClusterRate)
	hosting := smItemNamed("ml.p4d.24xlarge", "Hosting", p4dHostingRate)
	notebook := smItemNamed("ml.p4d.24xlarge", "Notebook", p4dHostingRate)

	orderings := [][]string{
		{cluster, hosting, notebook},
		{hosting, cluster, notebook},
		{notebook, cluster, hosting},
		{notebook, hosting, cluster},
		{cluster, notebook, hosting},
		{hosting, notebook, cluster},
	}
	for i, list := range orderings {
		price, err := pickSageMakerRate(list)
		if err != nil {
			t.Fatalf("ordering %d: %v", i, err)
		}
		if math.Abs(price-25.251) > 1e-9 {
			t.Errorf("ordering %d: price = %v, want 25.251 regardless of order", i, price)
		}
	}
}

// TestPickSageMakerRateFor_HonoursCallerIntent verifies an explicit preference
// wins: someone pricing HyperPod should get the Cluster rate, and someone pricing
// an endpoint should get Hosting, from the identical price list.
func TestPickSageMakerRateFor_HonoursCallerIntent(t *testing.T) {
	list := []string{
		smItemNamed("ml.p4d.24xlarge", "Hosting", p4dHostingRate),
		smItemNamed("ml.p4d.24xlarge", "Cluster", p4dClusterRate),
	}
	for _, tc := range []struct {
		usage string
		want  float64
	}{
		{"Cluster", 25.910},
		{"Hosting", 25.251},
		{"Training", 25.251}, // absent → falls through to the next-best present
	} {
		price, err := pickSageMakerRateFor(list, tc.usage)
		if err != nil {
			t.Fatalf("prefer %s: %v", tc.usage, err)
		}
		if math.Abs(price-tc.want) > 1e-9 {
			t.Errorf("prefer %s: price = %v, want %v", tc.usage, price, tc.want)
		}
	}
}

// TestPickSageMakerRate_NeverPicksUpfrontFee locks in the behaviour the issue
// flagged as a trap worth a regression test. USE1-TrainingPlanUpfrontFee is a
// one-off reservation fee, not an hourly rate, and at $13.57 it is LOWER than the
// real $25.25/hr — so selecting it would report SageMaker as ~38% cheaper than
// the equivalent EC2 instance ($21.96/hr). truffle is safe only because that row
// carries no "component" attribute; this test makes that dependency explicit so a
// future change to component handling can't silently reintroduce the bug.
func TestPickSageMakerRate_NeverPicksUpfrontFee(t *testing.T) {
	// The upfront-fee row as AWS returns it: no component attribute.
	upfrontFee := `{
		"product": {"attributes": {
			"instanceName": "ml.p4d.24xlarge",
			"regionCode": "us-east-1",
			"usagetype": "USE1-TrainingPlanUpfrontFee:ml.p4d.24xlarge"
		}},
		"terms": {"OnDemand": {"A": {"priceDimensions": {"B": {
			"unit": "Quantity", "pricePerUnit": {"USD": "13.5700000000"}
		}}}}}
	}`

	list := []string{upfrontFee, smItemNamed("ml.p4d.24xlarge", "Hosting", p4dHostingRate)}
	price, err := pickSageMakerRate(list)
	if err != nil {
		t.Fatalf("pickSageMakerRate error: %v", err)
	}
	if math.Abs(price-25.251) > 1e-9 {
		t.Errorf("price = %v, want 25.251 — the $13.57 upfront fee is not an hourly rate", price)
	}

	// Even as the ONLY row it must not be reported as an hourly rate: with no
	// compute component present, an unpriced type is the honest answer.
	if got, err := pickSageMakerRate([]string{upfrontFee}); err == nil {
		t.Errorf("upfront fee alone returned %v, want an error rather than a bogus hourly rate", got)
	}
}

// TestSageMakerComponent verifies extraction, including the no-component case
// that protects against the upfront-fee row.
func TestSageMakerComponent(t *testing.T) {
	if got := sageMakerComponent(smItem("Hosting", "1.0")); got != "Hosting" {
		t.Errorf("component = %q, want Hosting", got)
	}
	if got := sageMakerComponent(`{"product":{"attributes":{}}}`); got != "" {
		t.Errorf("component = %q, want empty for a row with no component attribute", got)
	}
	if got := sageMakerComponent("not json"); got != "" {
		t.Errorf("component = %q, want empty for unparseable input", got)
	}
}

// usageOnlyPricer implements the plain SageMakerPricer (NOT the usage extension),
// standing in for an embedder's custom implementation or a test fake.
type usageOnlyPricer struct{ calls int }

func (p *usageOnlyPricer) SageMakerPrice(context.Context, string, string) (float64, error) {
	p.calls++
	return 7.5, nil
}

// TestSageMakerPriceFor_FallsBackForPlainPricer verifies the extension interface
// is optional: a pricer that predates SageMakerUsagePricer still works, and the
// usage is simply not honoured rather than erroring. This is what keeps the #107
// API addition non-breaking for existing embedders.
func TestSageMakerPriceFor_FallsBackForPlainPricer(t *testing.T) {
	c := &Client{}
	fake := &usageOnlyPricer{}
	c.SetSageMakerPricer(fake)

	price, err := c.SageMakerPriceFor(context.Background(), "ml.p4d.24xlarge", "us-east-1", UsageHyperPod)
	if err != nil {
		t.Fatalf("SageMakerPriceFor error: %v", err)
	}
	if math.Abs(price-7.5) > 1e-9 {
		t.Errorf("price = %v, want 7.5 from the injected pricer", price)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1 (fell back to SageMakerPrice)", fake.calls)
	}
}

// TestAWSSageMakerPricer_ImplementsUsagePricer pins that the default pricer
// satisfies the extension interface, so SageMakerPriceFor actually honours the
// usage on the production path rather than silently falling back.
func TestAWSSageMakerPricer_ImplementsUsagePricer(t *testing.T) {
	var p any = newAWSSageMakerPricer(aws.Config{})
	if _, ok := p.(SageMakerUsagePricer); !ok {
		t.Error("awsSageMakerPricer does not implement SageMakerUsagePricer")
	}
}

// TestSageMakerPriceFor_CacheKeyIncludesUsage verifies two usages don't share one
// cache entry — otherwise an inference lookup could return a cached HyperPod rate.
func TestSageMakerPriceFor_CacheKeyIncludesUsage(t *testing.T) {
	p := newAWSSageMakerPricer(aws.Config{})
	p.cache["ml.p4d.24xlarge\x00us-east-1\x00Hosting"] = cachedPrice{price: 25.251, fetched: time.Now()}
	p.cache["ml.p4d.24xlarge\x00us-east-1\x00Cluster"] = cachedPrice{price: 25.910, fetched: time.Now()}

	ctx := context.Background()
	hosting, err := p.SageMakerPriceFor(ctx, "ml.p4d.24xlarge", "us-east-1", UsageInference)
	if err != nil {
		t.Fatalf("inference: %v", err)
	}
	cluster, err := p.SageMakerPriceFor(ctx, "ml.p4d.24xlarge", "us-east-1", UsageHyperPod)
	if err != nil {
		t.Fatalf("hyperpod: %v", err)
	}
	if math.Abs(hosting-25.251) > 1e-9 || math.Abs(cluster-25.910) > 1e-9 {
		t.Errorf("hosting = %v (want 25.251), cluster = %v (want 25.910) — usages must not share a cache entry",
			hosting, cluster)
	}
}

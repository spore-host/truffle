package aws

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// stubPricer returns a fixed price per instance type for deterministic tests.
type stubPricer struct {
	prices map[string]float64
	err    error
}

func (s stubPricer) OnDemandPrice(_ context.Context, instanceType, _ string) (float64, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.prices[instanceType], nil
}

func TestParseOnDemandFromPriceItem(t *testing.T) {
	// Minimal but realistic Price List product JSON for c6i.4xlarge in us-east-1.
	item := `{
		"product": {"attributes": {"instanceType": "c6i.4xlarge", "regionCode": "us-east-1"}},
		"terms": {
			"OnDemand": {
				"ABCD1234": {
					"priceDimensions": {
						"ABCD1234.JRTCKXETXF": {
							"unit": "Hrs",
							"pricePerUnit": {"USD": "0.6800000000"}
						}
					}
				}
			}
		}
	}`

	price, ok := parseOnDemandFromPriceItem(item)
	if !ok {
		t.Fatal("parseOnDemandFromPriceItem returned ok=false for valid product")
	}
	if math.Abs(price-0.68) > 1e-9 {
		t.Errorf("price = %v, want 0.68", price)
	}
}

func TestParseOnDemandFromPriceItem_SkipsZeroAndJunk(t *testing.T) {
	// A $0.00 placeholder dimension should be skipped (no positive price).
	zero := `{"terms":{"OnDemand":{"X":{"priceDimensions":{"Y":{"unit":"Hrs","pricePerUnit":{"USD":"0.0000000000"}}}}}}}`
	if _, ok := parseOnDemandFromPriceItem(zero); ok {
		t.Error("expected ok=false for $0.00 placeholder, got ok=true")
	}

	if _, ok := parseOnDemandFromPriceItem("not json"); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

func TestStaticOnDemandPricer(t *testing.T) {
	// The static pricer returns a known table value for a type it actually has.
	//
	// This test previously documented the pricer as one that "never errors."
	// That was the defect in #114: never erroring meant a table miss produced a
	// family-based guess instead, and callers could not tell a real rate from a
	// fabricated one. It now errors on a miss — see the cases below.
	p := staticOnDemandPricer{}
	price, err := p.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("static pricer error: %v", err)
	}
	if price <= 0 {
		t.Errorf("static price = %v, want > 0", price)
	}
}

// TestStaticOnDemandPricer_ErrorsOnTableMiss is the #114 regression. Each case
// used to return a plausible number with a nil error.
func TestStaticOnDemandPricer_ErrorsOnTableMiss(t *testing.T) {
	p := staticOnDemandPricer{}

	for _, tc := range []struct {
		name         string
		instanceType string
		region       string
		wasReturning string // what the old GetEC2HourlyRate path produced
	}{
		{
			"unknown family in a known region",
			"hpc7a.96xlarge", "us-east-2",
			"$0.20 (base 0.10 × unknown-size 2.0) against a real $7.20",
		},
		{
			"current GPU family absent from the table",
			"p5.48xlarge", "us-east-1",
			"$9.60 (unknown-family 0.10 × 48xlarge 96.0) against a real $55.04",
		},
		{
			"known type in a region the table does not cover",
			"c6i.4xlarge", "sa-east-1",
			"the us-east-1 price, silently substituted for a different region",
		},
		{
			"unparseable type",
			"garbage", "us-east-1",
			"$0.10",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price, err := p.OnDemandPrice(context.Background(), tc.instanceType, tc.region)
			if err == nil {
				t.Errorf("no error for %s in %s; got $%.4f — old behavior returned %s",
					tc.instanceType, tc.region, price, tc.wasReturning)
			}
			if price != 0 {
				t.Errorf("price = %v, want 0 alongside the error", price)
			}
		})
	}
}

// TestStaticOnDemandPricer_NormalizesInput verifies the exact lookup still
// tolerates the casing and whitespace GetEC2HourlyRate did, so replacing it does
// not turn working callers into errors.
func TestStaticOnDemandPricer_NormalizesInput(t *testing.T) {
	p := staticOnDemandPricer{}
	want, err := p.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("baseline lookup failed: %v", err)
	}

	for _, tc := range []struct{ instanceType, region string }{
		{"C6I.4XLARGE", "US-EAST-1"},
		{" c6i.4xlarge ", " us-east-1 "},
	} {
		got, err := p.OnDemandPrice(context.Background(), tc.instanceType, tc.region)
		if err != nil {
			t.Errorf("OnDemandPrice(%q, %q): %v", tc.instanceType, tc.region, err)
			continue
		}
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("OnDemandPrice(%q, %q) = %v, want %v", tc.instanceType, tc.region, got, want)
		}
	}
}

func TestFallbackPricer_UsesFallbackOnPrimaryFailure(t *testing.T) {
	f := &fallbackPricer{
		primary:  stubPricer{prices: map[string]float64{}}, // returns 0 → triggers fallback
		fallback: stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}},
	}
	price, err := f.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("fallback pricer error: %v", err)
	}
	if math.Abs(price-0.68) > 1e-9 {
		t.Errorf("price = %v, want 0.68 (from fallback)", price)
	}
}

func TestFallbackPricer_PrefersPrimary(t *testing.T) {
	f := &fallbackPricer{
		primary:  stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.70}},
		fallback: stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}},
	}
	price, _ := f.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if math.Abs(price-0.70) > 1e-9 {
		t.Errorf("price = %v, want 0.70 (from primary)", price)
	}
}

// TestFallbackPricer_ErrorsWhenBothFail verifies an unpriceable type produces an
// error rather than a number. This is the outer half of #114: even with a static
// pricer that errors, a fallbackPricer that swallowed the failure would still
// hand the caller $0.00 with a nil error.
func TestFallbackPricer_ErrorsWhenBothFail(t *testing.T) {
	f := &fallbackPricer{
		primary:  stubPricer{err: errors.New("no on-demand price found for hpc7a.96xlarge in us-west-2")},
		fallback: staticOnDemandPricer{},
	}
	price, err := f.OnDemandPrice(context.Background(), "hpc7a.96xlarge", "us-west-2")
	if err == nil {
		t.Fatalf("no error when neither source has a price; got $%.4f", price)
	}
	if price != 0 {
		t.Errorf("price = %v, want 0 alongside the error", price)
	}
	// The primary's error says *why* the live lookup failed, which is what a
	// caller needs to act on; the fallback's is context.
	if !strings.Contains(err.Error(), "no on-demand price found for hpc7a.96xlarge") {
		t.Errorf("error lost the live lookup's reason: %v", err)
	}
	if !strings.Contains(err.Error(), "static fallback also unavailable") {
		t.Errorf("error does not mention the fallback also failed: %v", err)
	}
}

// TestFallbackPricer_ReportsSource verifies a caller can tell a live rate from a
// degraded one. Without this, a cost estimate that gates real spending cannot
// distinguish the authoritative rate from a possibly-stale table entry.
func TestFallbackPricer_ReportsSource(t *testing.T) {
	live := &fallbackPricer{
		primary:  stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.70}},
		fallback: staticOnDemandPricer{},
	}
	if _, src, err := live.OnDemandPriceWithSource(context.Background(), "c6i.4xlarge", "us-east-1"); err != nil || src != PriceSourceLive {
		t.Errorf("source = %q (err %v), want %q", src, err, PriceSourceLive)
	}

	degraded := &fallbackPricer{
		primary:  stubPricer{err: errors.New("price list unreachable")},
		fallback: staticOnDemandPricer{},
	}
	if _, src, err := degraded.OnDemandPriceWithSource(context.Background(), "c6i.4xlarge", "us-east-1"); err != nil || src != PriceSourceStatic {
		t.Errorf("source = %q (err %v), want %q", src, err, PriceSourceStatic)
	}
}

// TestOnDemandPriceWithSource_InjectedPricer verifies an embedder's pricer that
// only satisfies OnDemandPricer still works, reporting an unknown source rather
// than claiming its rate is live.
func TestOnDemandPriceWithSource_InjectedPricer(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"m6i.large": 0.096}})

	price, src, err := c.OnDemandPriceWithSource(context.Background(), "m6i.large", "us-east-1")
	if err != nil {
		t.Fatalf("OnDemandPriceWithSource: %v", err)
	}
	if math.Abs(price-0.096) > 1e-9 {
		t.Errorf("price = %v, want 0.096", price)
	}
	if src != PriceSourceUnknown {
		t.Errorf("source = %q, want %q — an injected pricer's provenance is not knowable", src, PriceSourceUnknown)
	}
}

func TestSetOnDemandPricer_OverridesDefault(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"m6i.large": 0.096}})

	price, err := c.OnDemandPrice(context.Background(), "m6i.large", "us-east-1")
	if err != nil {
		t.Fatalf("OnDemandPrice error: %v", err)
	}
	if math.Abs(price-0.096) > 1e-9 {
		t.Errorf("price = %v, want 0.096", price)
	}
}

func TestHourlyRate_OnDemand(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}})

	for _, model := range []string{"on-demand", "ondemand", "ON-DEMAND", ""} {
		rate, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", model)
		if err != nil {
			t.Fatalf("HourlyRate(%q) error: %v", model, err)
		}
		if math.Abs(rate-0.68) > 1e-9 {
			t.Errorf("HourlyRate(%q) = %v, want 0.68", model, rate)
		}
	}
}

func TestHourlyRate_OnDemandUnavailable(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{}}) // returns 0

	if _, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", "on-demand"); err == nil {
		t.Error("expected error when on-demand price is unavailable, got nil")
	}
}

func TestHourlyRate_RejectsReservedAndUnknown(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}})

	if _, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", "reserved"); err == nil {
		t.Error("expected error for reserved model, got nil")
	}
	if _, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", "bogus"); err == nil {
		t.Error("expected error for unknown model, got nil")
	}
}

// TestStaticOnDemandPricer_GravitonCoverage is the truffle#175 regression, and
// the reported repro verbatim: `c7g.2xlarge` in `us-west-2`.
//
// The static table had **no** Graviton family in **any** region — no c6g–c9g, no
// m6g–m8g, no r6g–r9g. So for the entire Graviton line the fallback could not
// fire, and a throttled Price List plus a `--cost-limit` meant a refused launch:
// the exact inversion of the intended degradation order, since the table exists
// precisely for "Price List unavailable" and was missing the architecture most of
// this suite's users run.
//
// Asserting on every (family, size, region) rather than one spot check, because
// the defect was *absence* — and absence is the one thing a single happy-path
// case cannot detect.
func TestStaticOnDemandPricer_GravitonCoverage(t *testing.T) {
	p := staticOnDemandPricer{}

	// The eight regions the table covers. Hardcoded rather than ranged over
	// EC2Pricing, so dropping a region fails this test instead of shrinking it.
	regions := []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"eu-west-1", "eu-central-1", "ap-northeast-1", "ap-southeast-1",
	}
	// The sizes the issue asked for.
	sizes := []string{"large", "xlarge", "2xlarge", "4xlarge", "8xlarge"}
	// Mature generations, required in EVERY covered region. AWS offers all nine
	// everywhere the table reaches, so a miss here is a table defect.
	mature := []string{
		"c6g", "c7g", "c8g",
		"m6g", "m7g", "m8g",
		"r6g", "r7g", "r8g",
	}

	for _, region := range regions {
		for _, fam := range mature {
			for _, size := range sizes {
				it := fam + "." + size
				t.Run(region+"/"+it, func(t *testing.T) {
					price, err := p.OnDemandPrice(context.Background(), it, region)
					if err != nil {
						t.Fatalf("no static price for %s in %s: %v — a throttled Price List "+
							"would refuse this launch outright when a cost limit is set (#175)", it, region, err)
					}
					if price <= 0 {
						t.Errorf("static price for %s in %s = %v, want > 0", it, region, price)
					}
				})
			}
		}
	}
}

// TestStaticOnDemandPricer_NewestGravitonCoverage covers c9g and r9g, which the
// issue also asked for but which AWS does not yet offer in every region the
// table reaches — c9g in five of the eight, r9g in four.
//
// So this does NOT demand availability that does not exist, which would make the
// test a liability the moment it was right to omit a rate. It asserts the two
// things that are actually invariants:
//
//  1. No partial size ladder. A family present in a region must have ALL the
//     asked sizes, because a half-populated family is the shape a hand-edit or a
//     truncated fetch leaves behind, and it fails at launch for exactly the one
//     size somebody picked.
//  2. A floor on breadth, not an exact list. Pinning the regions would turn a
//     normal AWS rollout into a red build; a minimum catches the regression
//     (coverage collapsing) while letting expansion pass untouched.
func TestStaticOnDemandPricer_NewestGravitonCoverage(t *testing.T) {
	p := staticOnDemandPricer{}
	regions := []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"eu-west-1", "eu-central-1", "ap-northeast-1", "ap-southeast-1",
	}
	sizes := []string{"large", "xlarge", "2xlarge", "4xlarge", "8xlarge"}

	// Floors as measured at PricesAsOf 2026-10-07: c9g in 5 regions, r9g in 4.
	for fam, minRegions := range map[string]int{"c9g": 5, "r9g": 4} {
		t.Run(fam, func(t *testing.T) {
			covered := 0
			for _, region := range regions {
				found := pricedSizes(p, fam, region, sizes)
				switch found {
				case 0:
					continue // not offered here; not a defect
				case len(sizes):
					covered++
				default:
					t.Errorf("%s in %s has %d of %d sizes — a partial ladder fails at launch "+
						"for whichever size is missing", fam, region, found, len(sizes))
				}
			}
			if covered < minRegions {
				t.Errorf("%s priced in %d regions, want at least %d — coverage went backwards (#175)",
					fam, covered, minRegions)
			}
		})
	}
}

// TestFallbackPricer_GravitonSurvivesThrottling is #175's outer half: the
// coverage above only matters if the fallback actually engages on the error the
// reporter saw. The primary here fails with the real ThrottlingException text
// from the issue.
func TestFallbackPricer_GravitonSurvivesThrottling(t *testing.T) {
	throttled := errors.New("price list GetProducts for c7g.2xlarge in us-west-2: " +
		"operation error Pricing: GetProducts, exceeded maximum number of attempts, 3, " +
		"https response error StatusCode: 400, ThrottlingException: Rate exceeded")

	f := &fallbackPricer{primary: stubPricer{err: throttled}, fallback: staticOnDemandPricer{}}
	price, source, err := f.OnDemandPriceWithSource(context.Background(), "c7g.2xlarge", "us-west-2")
	if err != nil {
		t.Fatalf("throttled Price List did not degrade to the static table: %v", err)
	}
	if price <= 0 {
		t.Errorf("price = %v, want > 0", price)
	}
	// The caller must be able to tell a degraded price from a live one — that
	// distinction is why #114 made the static pricer error rather than guess.
	if source != PriceSourceStatic {
		t.Errorf("source = %v, want %v", source, PriceSourceStatic)
	}
}

// pricedSizes counts how many of sizes the pricer can answer for one family in
// one region. Extracted so the partial-ladder rule above can be exercised
// against a pricer with a deliberate hole — the embedded table has none, so an
// inline check would be an assertion nothing could ever trip.
func pricedSizes(p OnDemandPricer, fam, region string, sizes []string) int {
	n := 0
	for _, size := range sizes {
		if price, err := p.OnDemandPrice(context.Background(), fam+"."+size, region); err == nil && price > 0 {
			n++
		}
	}
	return n
}

// holePricer answers like the static table except for one instance type, which
// it refuses — the shape a truncated fetch or a hand-edit leaves behind.
type holePricer struct {
	inner   OnDemandPricer
	missing string
}

func (h holePricer) OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error) {
	if instanceType == h.missing {
		return 0, errors.New("no static price for " + instanceType + " in " + region)
	}
	return h.inner.OnDemandPrice(ctx, instanceType, region)
}

// TestPricedSizes_DetectsAPartialLadder is the gate on the gate: without it,
// TestStaticOnDemandPricer_NewestGravitonCoverage's partial-ladder arm is
// unreachable, since every family in the real table is complete.
func TestPricedSizes_DetectsAPartialLadder(t *testing.T) {
	sizes := []string{"large", "xlarge", "2xlarge", "4xlarge", "8xlarge"}

	if got := pricedSizes(staticOnDemandPricer{}, "c9g", "us-east-1", sizes); got != len(sizes) {
		t.Fatalf("precondition: complete ladder counted %d of %d", got, len(sizes))
	}
	holed := holePricer{inner: staticOnDemandPricer{}, missing: "c9g.4xlarge"}
	if got := pricedSizes(holed, "c9g", "us-east-1", sizes); got != len(sizes)-1 {
		t.Errorf("one missing size counted %d of %d — the partial-ladder arm would never fire",
			got, len(sizes))
	}
}

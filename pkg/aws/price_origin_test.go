package aws

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	libpricing "github.com/spore-host/libs/pricing"
	"github.com/spore-host/truffle/pkg/testutil"
	"gopkg.in/yaml.v3"
)

// assertStaticOrigin is the #184 contract for one static price: it says it is
// static, carries the collection date of the static table, and carries the
// reason the live Price List was not used — containing wantReason.
func assertStaticOrigin(t *testing.T, where string, o PriceOrigin, wantReason string) {
	t.Helper()
	if o.PriceSource != PriceSourceStatic {
		t.Errorf("%s: PriceSource = %q, want static", where, o.PriceSource)
	}
	if o.PriceAsOf != libpricing.PricesAsOf {
		t.Errorf("%s: PriceAsOf = %q, want libs/pricing.PricesAsOf %q", where, o.PriceAsOf, libpricing.PricesAsOf)
	}
	if !strings.Contains(o.PriceFallbackReason, wantReason) {
		t.Errorf("%s: PriceFallbackReason = %q, want it to contain %q", where, o.PriceFallbackReason, wantReason)
	}
}

// TestFallbackPricer_StaticPriceKeepsWhyAndWhen is the #184 plumbing gate on the
// single-price path. The live lookup's error used to survive only when the
// static fallback ALSO failed; on the successful fallback — the only case that
// prints a static price — it was dropped, so no command could say why.
func TestFallbackPricer_StaticPriceKeepsWhyAndWhen(t *testing.T) {
	const cause = "api error ThrottlingException: Rate exceeded"
	f := &fallbackPricer{primary: stubPricer{err: errors.New(cause)}, fallback: staticOnDemandPricer{}}
	price, origin, err := f.OnDemandPriceWithOrigin(context.Background(), "m5.large", "us-east-1")
	if err != nil || price <= 0 {
		t.Fatalf("OnDemandPriceWithOrigin = %v, %v; want a static price", price, err)
	}
	assertStaticOrigin(t, "fallbackPricer", origin, cause)

	// The same through the Client, which every command uses.
	c := NewClientFromConfig(aws.Config{})
	c.SetOnDemandPricer(f)
	_, origin, err = c.OnDemandPriceWithOrigin(context.Background(), "m5.large", "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	assertStaticOrigin(t, "Client.OnDemandPriceWithOrigin", origin, cause)

	// A live price has no date or reason to give.
	f.primary = stubPricer{prices: map[string]float64{"m5.large": 0.096}}
	if _, origin, _ := f.OnDemandPriceWithOrigin(context.Background(), "m5.large", "us-east-1"); origin != LivePriceOrigin() {
		t.Errorf("live origin = %+v, want %+v", origin, LivePriceOrigin())
	}
}

// TestOnDemandPrices_StaticPriceKeepsWhyAndWhen is the same gate on the batch
// path `truffle find` and `search --show-price` use: both the "live had no
// rate" and the "live failed" reasons reach the quote.
func TestOnDemandPrices_StaticPriceKeepsWhyAndWhen(t *testing.T) {
	fake := &fakeGetProducts{prices: map[string]map[string]float64{"c7g.2xlarge": {"eu-west-1": 0.3}}}
	got := clientWithFakePriceList(fake).OnDemandPrices(context.Background(), []PriceKey{
		{"c7g.2xlarge", "eu-west-1"}, {"c7g.2xlarge", "us-west-2"},
	})
	if q := got[PriceKey{"c7g.2xlarge", "eu-west-1"}]; q.Origin != LivePriceOrigin() {
		t.Errorf("eu-west-1 origin = %+v, want live", q.Origin)
	}
	assertStaticOrigin(t, "batch, live had no rate", got[PriceKey{"c7g.2xlarge", "us-west-2"}].Origin,
		"returned no on-demand price for c7g.2xlarge in us-west-2")

	c := NewClientFromConfig(aws.Config{})
	c.SetOnDemandPricer(&fallbackPricer{primary: stubPricer{err: errors.New("AccessDeniedException")}, fallback: staticOnDemandPricer{}})
	q := c.OnDemandPrices(context.Background(), []PriceKey{{"m5.large", "us-east-1"}})[PriceKey{"m5.large", "us-east-1"}]
	assertStaticOrigin(t, "batch, live failed", q.Origin, "AccessDeniedException")
	if q.Source != PriceSourceStatic {
		t.Errorf("quote Source = %q, want it to mirror Origin", q.Source)
	}
}

// TestGetSpotPricing_StaticOnDemandKeepsWhyAndWhen: the on-demand rate behind
// `truffle spot`'s On-Demand and Savings columns carries its origin too; it
// used to be read through OnDemandPrice, which has no source at all.
func TestGetSpotPricing_StaticOnDemandKeepsWhyAndWhen(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()
	c := NewClientFromConfig(env.AWSConfig)
	c.SetOnDemandPricer(&fallbackPricer{primary: stubPricer{err: errors.New("no credentials")}, fallback: staticOnDemandPricer{}})

	instances, err := c.SearchInstanceTypes(ctx, []string{"us-east-1"}, regexp.MustCompile(`^t3\.micro$`), FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) == 0 {
		t.Fatal("t3.micro not in the seeded catalog; the gate needs one priced spot row")
	}
	prices, err := c.GetSpotPricing(ctx, instances, SpotOptions{LookbackHours: 24, ShowSavings: true})
	if err != nil || len(prices) == 0 {
		t.Fatalf("GetSpotPricing = %d rows, %v", len(prices), err)
	}
	for _, p := range prices {
		if p.OnDemandPrice <= 0 {
			t.Fatalf("no on-demand price on %+v", p)
		}
		assertStaticOrigin(t, "SpotPriceResult "+p.AvailabilityZone, p.PriceOrigin, "no credentials")
	}
}

// TestPriceOrigin_SerializedWithEveryPrice: each result type that carries an
// on-demand price carries its origin in JSON and YAML under price_source,
// price_as_of and price_fallback_reason (#184).
func TestPriceOrigin_SerializedWithEveryPrice(t *testing.T) {
	origin := StaticPriceOrigin("live AWS Price List unavailable: boom")
	price := 0.5
	values := map[string]any{
		"InstanceTypeResult": InstanceTypeResult{InstanceType: "m5.large", OnDemandPrice: price, PriceOrigin: origin},
		"SpotPriceResult":    SpotPriceResult{InstanceType: "m5.large", OnDemandPrice: price, PriceOrigin: origin},
		"Obtainability":      Obtainability{InstanceType: "m5.large", OnDemandPrice: &price, PriceOrigin: origin},
	}
	for name, v := range values {
		for format, marshal := range map[string]func(any) ([]byte, error){"json": json.Marshal, "yaml": yaml.Marshal} {
			raw, err := marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"price_source", "static", "price_as_of", libpricing.PricesAsOf, "price_fallback_reason", "boom"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("%s %s lacks %q: %s", name, format, want, raw)
				}
			}
		}
	}
}

// TestPriceOrigin_Label pins the one wording every renderer shares.
func TestPriceOrigin_Label(t *testing.T) {
	static := StaticPriceOrigin("live AWS Price List unavailable: boom")
	want := "$0.7140/hr on-demand (static, collected " + libpricing.PricesAsOf + ": live AWS Price List unavailable: boom)"
	if got := DescribeOnDemandPrice(0.714, static); got != want {
		t.Errorf("static = %q, want %q", got, want)
	}
	if got := DescribeOnDemandPrice(0.714, LivePriceOrigin()); got != "$0.7140/hr on-demand (live)" {
		t.Errorf("live = %q", got)
	}
	if got := StaticPriceOrigin("").PriceFallbackReason; got == "" {
		t.Error("an empty reason was left blank; it must say it was not recorded")
	}
	// An injected pricer that reports only "static" can't vouch for a date.
	if got := originFromSource(PriceSourceStatic).Label(); !strings.Contains(got, "collection date unknown") {
		t.Errorf("injected static label = %q, want it to admit the date is unknown", got)
	}
}

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	libpricing "github.com/spore-host/libs/pricing"
	"github.com/spore-host/truffle/pkg/aws"
)

// staticPriceReason is the fallback reason the #184 gates look for verbatim.
const staticPriceReason = "live AWS Price List unavailable: api error ThrottlingException: Rate exceeded"

// staticPriceRenderers maps every function in this package that renders an
// on-demand price (or a figure derived from one, like spot savings) to a
// function returning what it prints for one static-priced value. The
// internal/hygiene gate discovers this package's price-reading functions and
// fails if a render site is missing here (#184).
var staticPriceRenderers = map[string]func(t *testing.T) string{
	"printObtainability": func(t *testing.T) string { return renderObtainabilityAs(t, "table") },
	"writeObtainability": func(t *testing.T) string {
		return renderObtainabilityAs(t, "json") + renderObtainabilityAs(t, "yaml")
	},
	"reportPricing": func(t *testing.T) string {
		results := []aws.InstanceTypeResult{{InstanceType: "m5.large", Region: "us-east-1"}}
		p := &oneQuotePricer{quote: aws.PriceQuote{Price: 0.096, Source: aws.PriceSourceStatic, Origin: aws.StaticPriceOrigin(staticPriceReason)}}
		o := priceResults(t.Context(), p, results, nil, "ec2")
		return captureOutput(t, func() { _ = reportPricing(o, time.Minute) })
	},
	"printSpotSummary": func(t *testing.T) string {
		return captureOutput(t, func() {
			printSpotSummary([]aws.SpotPriceResult{{
				InstanceType: "m5.large", Region: "us-east-1", AvailabilityZone: "us-east-1a",
				SpotPrice: 0.04, OnDemandPrice: 0.096, SavingsPercent: 58,
				PriceOrigin: aws.StaticPriceOrigin(staticPriceReason),
			}})
		})
	},
}

func renderObtainabilityAs(t *testing.T, format string) string {
	t.Helper()
	price := 0.714
	var buf bytes.Buffer
	if err := writeObtainability(&buf, format, &aws.Obtainability{
		InstanceType: "m7i.2xlarge", Region: "us-east-1",
		OfferedAZs: []string{"us-east-1a"}, TotalAZs: 1,
		OnDemandPrice: &price, OnDemandPriceSource: aws.PriceSourceStatic,
		PriceOrigin: aws.StaticPriceOrigin(staticPriceReason),
	}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// oneQuotePricer answers every key with one quote.
type oneQuotePricer struct{ quote aws.PriceQuote }

func (f *oneQuotePricer) OnDemandPrices(_ context.Context, keys []aws.PriceKey) map[aws.PriceKey]aws.PriceQuote {
	out := make(map[aws.PriceKey]aws.PriceQuote, len(keys))
	for _, k := range keys {
		out[k] = f.quote
	}
	return out
}

func (f *oneQuotePricer) SageMakerPrices(ctx context.Context, keys []aws.PriceKey) map[aws.PriceKey]aws.PriceQuote {
	return f.OnDemandPrices(ctx, keys)
}

// TestStaticPriceIsLabelledInEveryCommand is the #184 gate for cmd: every
// render site, given a static price, says it is static, when the static table
// was collected, and why the live Price List wasn't used.
func TestStaticPriceIsLabelledInEveryCommand(t *testing.T) {
	for name, render := range staticPriceRenderers {
		t.Run(name, func(t *testing.T) {
			out := render(t)
			for _, want := range []string{"static", libpricing.PricesAsOf, staticPriceReason} {
				if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
					t.Errorf("%s output lacks %q for a static price:\n%s", name, want, out)
				}
			}
		})
	}
}

// TestAvailable_StaticPriceRow pins the exact `available` row wording (#184).
func TestAvailable_StaticPriceRow(t *testing.T) {
	out := renderObtainabilityAs(t, "table")
	want := "$0.7140/hr on-demand (static, collected " + libpricing.PricesAsOf + ": " + staticPriceReason + ")"
	if !strings.Contains(out, want) {
		t.Errorf("available price row lacks %q:\n%s", want, out)
	}
}

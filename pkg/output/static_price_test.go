package output

import (
	"strings"
	"testing"

	libpricing "github.com/spore-host/libs/pricing"
	"github.com/spore-host/truffle/pkg/aws"
)

// staticPriceReason is the fallback reason the #184 gates look for verbatim.
const staticPriceReason = "live AWS Price List unavailable: api error ThrottlingException: Rate exceeded"

// staticPriceRenderers maps every printer that renders an on-demand price (or a
// figure derived from one) to a function that renders one static-priced row.
// The internal/hygiene gate discovers the price-reading functions in this
// package and fails if one is missing here, so a new printer can't ship a bare
// static price unnoticed (#184).
var staticPriceRenderers = map[string]func(p *Printer){
	"printTable": func(p *Printer) {
		_ = p.PrintTableWithOptions([]aws.InstanceTypeResult{staticInstance()}, TableOptions{ShowPrice: true})
	},
	"PrintCSV":       func(p *Printer) { _ = p.PrintCSV([]aws.InstanceTypeResult{staticInstance()}) },
	"PrintJSON":      func(p *Printer) { _ = p.PrintJSON([]aws.InstanceTypeResult{staticInstance()}) },
	"PrintYAML":      func(p *Printer) { _ = p.PrintYAML([]aws.InstanceTypeResult{staticInstance()}) },
	"PrintSpotTable": func(p *Printer) { _ = p.PrintSpotTable([]aws.SpotPriceResult{staticSpot()}) },
	"PrintSpotCSV":   func(p *Printer) { _ = p.PrintSpotCSV([]aws.SpotPriceResult{staticSpot()}) },
	"PrintSpotJSON":  func(p *Printer) { _ = p.PrintSpotJSON([]aws.SpotPriceResult{staticSpot()}) },
	"PrintSpotYAML":  func(p *Printer) { _ = p.PrintSpotYAML([]aws.SpotPriceResult{staticSpot()}) },
}

func staticInstance() aws.InstanceTypeResult {
	return aws.InstanceTypeResult{
		InstanceType: "m5.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8192, Architecture: "x86_64",
		OnDemandPrice: 0.096, PriceOrigin: aws.StaticPriceOrigin(staticPriceReason),
	}
}

func staticSpot() aws.SpotPriceResult {
	return aws.SpotPriceResult{
		InstanceType: "m5.large", Region: "us-east-1", AvailabilityZone: "us-east-1a",
		SpotPrice: 0.04, OnDemandPrice: 0.096, SavingsPercent: 58, Timestamp: "2026-11-02T12:00:00Z",
		PriceOrigin: aws.StaticPriceOrigin(staticPriceReason),
	}
}

// TestStaticPriceIsLabelledInEveryPrinter is the #184 gate for pkg/output:
// every printer that shows an on-demand price, when that price is static,
// shows that it is static, the date the static table was collected, and why
// the live Price List wasn't used. Table formats mark the cell and explain it
// in a footer; CSV/JSON/YAML carry price_source, price_as_of and
// price_fallback_reason.
func TestStaticPriceIsLabelledInEveryPrinter(t *testing.T) {
	for name, render := range staticPriceRenderers {
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() { render(NewPrinter(false)) })
			for _, want := range []string{"static", libpricing.PricesAsOf, staticPriceReason} {
				if !strings.Contains(out, want) {
					t.Errorf("%s output lacks %q for a static price:\n%s", name, want, out)
				}
			}
		})
	}
}

// TestLivePriceIsNotMarkedStatic: the marker and footer appear only for a
// static rate, so they keep meaning something.
func TestLivePriceIsNotMarkedStatic(t *testing.T) {
	r := staticInstance()
	r.PriceOrigin = aws.LivePriceOrigin()
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions([]aws.InstanceTypeResult{r}, TableOptions{ShowPrice: true})
	})
	if strings.Contains(out, "static") || strings.Contains(out, libpricing.PricesAsOf) {
		t.Errorf("live price rendered as static:\n%s", out)
	}
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/spf13/cobra"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
)

// fakeBatchPricer answers from quotes and records which keys it was asked for.
type fakeBatchPricer struct {
	quotes map[aws.PriceKey]aws.PriceQuote
	asked  []aws.PriceKey
}

func (f *fakeBatchPricer) OnDemandPrices(_ context.Context, keys []aws.PriceKey) map[aws.PriceKey]aws.PriceQuote {
	f.asked = append(f.asked, keys...)
	out := make(map[aws.PriceKey]aws.PriceQuote)
	for _, k := range keys {
		out[k] = f.quotes[k]
	}
	return out
}

func (f *fakeBatchPricer) SageMakerPrices(ctx context.Context, keys []aws.PriceKey) map[aws.PriceKey]aws.PriceQuote {
	return f.OnDemandPrices(ctx, keys)
}

func pricesOf(rs []aws.InstanceTypeResult) map[aws.PriceKey]aws.PriceQuote {
	out := make(map[aws.PriceKey]aws.PriceQuote)
	for _, r := range rs {
		out[aws.PriceKey{InstanceType: r.InstanceType, Region: r.Region}] = aws.PriceQuote{Price: r.OnDemandPrice, Source: aws.PriceSourceLive}
	}
	return out
}

func unpriced(rs []aws.InstanceTypeResult) []aws.InstanceTypeResult {
	out := append([]aws.InstanceTypeResult(nil), rs...)
	for i := range out {
		out[i].OnDemandPrice = 0
	}
	return out
}

// mixedResults adds what turinResults lacks: older generations, and two
// best-fit types whose order only price decides.
func mixedResults() []aws.InstanceTypeResult {
	mk := func(it, region string, vcpu int32, memGiB int64, price float64) aws.InstanceTypeResult {
		return aws.InstanceTypeResult{InstanceType: it, Region: region, VCPUs: vcpu, MemoryMiB: memGiB * 1024, OnDemandPrice: price}
	}
	return []aws.InstanceTypeResult{
		mk("m7g.2xlarge", "us-west-2", 8, 32, 0.3264),
		mk("m8g.2xlarge", "us-west-2", 8, 32, 0.3590),
		mk("m8g.2xlarge", "eu-west-1", 8, 32, 0.4010),
		mk("m6g.2xlarge", "us-east-1", 8, 32, 0.3080), // cheapest exact fit, oldest
		mk("r8g.2xlarge", "us-west-2", 8, 64, 0.4713),
		mk("c8g.4xlarge", "us-west-2", 16, 32, 0.6381),
		mk("m8g.48xlarge", "us-west-2", 192, 768, 8.6170),
		mk("c7g.16xlarge", "us-west-2", 64, 128, 2.3123),
	}
}

// TestPickFirstPriceCandidates_SameAnswerAsPricingEverything is truffle#210's
// "price only what is needed": --pick-first prints one type, so it prices only
// the types that can still win. Price is a tiebreak in closest-fit order, so
// the candidates must include every type tied on the keys before price, or the
// pick changes. Checked for every sort preference against pricing all rows.
func TestPickFirstPriceCandidates_SameAnswerAsPricingEverything(t *testing.T) {
	cases := []struct {
		name    string
		results func() []aws.InstanceTypeResult
		f       aws.FilterOptions
		maxWant int // candidate types expected at most (0 = no bound)
	}{
		{"turin closest fit", turinResults, aws.FilterOptions{MinVCPUs: 16, MinMemory: 64}, 1},
		{"graviton closest fit, price breaks a 3-way tie", mixedResults, aws.FilterOptions{MinVCPUs: 8, MinMemory: 32}, 3},
		{"no size terms: newest first", mixedResults, aws.FilterOptions{}, 4},
	}
	prefs := []find.SortPreference{find.SortDefault, find.SortCheapest, find.SortExpensive, find.SortPerformant, find.SortNewest}
	for _, tc := range cases {
		for _, pref := range prefs {
			t.Run(fmt.Sprintf("%s/%v", tc.name, pref), func(t *testing.T) {
				full := tc.results()
				sortFindResults(full, tc.f, pref)

				rs := unpriced(tc.results())
				want := pickFirstPriceCandidates(rs, tc.f, pref)
				p := &fakeBatchPricer{quotes: pricesOf(tc.results())}
				priceResults(context.Background(), p, rs, want, "ec2")
				sortFindResults(rs, tc.f, pref)

				if rs[0].InstanceType != full[0].InstanceType {
					t.Errorf("pick = %s, want %s (candidates %v)", rs[0].InstanceType, full[0].InstanceType, keysOf(want))
				}
				if pref == find.SortDefault && tc.maxWant > 0 && (want == nil || len(want) > tc.maxWant) {
					t.Errorf("priced %d types (%v), want at most %d", len(want), keysOf(want), tc.maxWant)
				}
			})
		}
	}
}

// TestPickFirstPriceCandidates_PriceTiebreakNeedsAllTied pins the case the
// first test would miss if candidates were "the first best-fit type": three
// exact fits, and the cheapest (m6g, oldest) must win on price.
func TestPickFirstPriceCandidates_PriceTiebreakNeedsAllTied(t *testing.T) {
	f := aws.FilterOptions{MinVCPUs: 8, MinMemory: 32}
	got := keysOf(pickFirstPriceCandidates(mixedResults(), f, find.SortDefault))
	if want := "m6g.2xlarge,m7g.2xlarge,m8g.2xlarge"; strings.Join(got, ",") != want {
		t.Errorf("candidates = %v, want %s", got, want)
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPriceResults_PricesOnlyWantedRows: rows outside want are not looked up.
func TestPriceResults_PricesOnlyWantedRows(t *testing.T) {
	rs := unpriced(turinResults())
	p := &fakeBatchPricer{quotes: pricesOf(turinResults())}
	priceResults(context.Background(), p, rs, map[string]bool{"m8a.4xlarge": true}, "ec2")
	if len(p.asked) != 2 {
		t.Errorf("asked for %d prices (%v), want 2 (m8a.4xlarge in two regions)", len(p.asked), p.asked)
	}
	for _, r := range rs {
		if (r.InstanceType == "m8a.4xlarge") != (r.OnDemandPrice > 0) {
			t.Errorf("%s@%s price %v", r.InstanceType, r.Region, r.OnDemandPrice)
		}
	}
}

// TestPricingTimeoutIsAnError is truffle#210's exit-status half: a run whose
// deadline expired during pricing exited 0 and called its unpriced rows
// "may be out of date". A timeout must fail the command and say it timed out;
// a type AWS simply publishes no price for must not.
func TestPricingTimeoutIsAnError(t *testing.T) {
	rs := unpriced(turinResults())
	q := pricesOf(turinResults())
	q[aws.PriceKey{InstanceType: "r8a.48xlarge", Region: "us-east-1"}] = aws.PriceQuote{Err: fmt.Errorf("price list GetProducts: %w", context.DeadlineExceeded)}
	q[aws.PriceKey{InstanceType: "r8a.48xlarge", Region: "us-west-2"}] = aws.PriceQuote{Err: errors.New("no on-demand price found")}
	q[aws.PriceKey{InstanceType: "c8a.8xlarge", Region: "us-west-2"}] = aws.PriceQuote{Price: 1.7, Source: aws.PriceSourceStatic}

	o := priceResults(context.Background(), &fakeBatchPricer{quotes: q}, rs, nil, "ec2")
	if o.rows != 8 || o.unpriced != 2 || o.timedOut != 1 || o.static != 1 {
		t.Errorf("outcome = %+v, want rows 8, unpriced 2, timedOut 1, static 1", o)
	}
	err := reportPricing(o, 5*time.Minute)
	if err == nil || !strings.Contains(err.Error(), "timed out after 5m0s") || !strings.Contains(err.Error(), "1 of 8 rows") {
		t.Errorf("reportPricing = %v, want a timeout error naming 1 of 8 rows", err)
	}

	// It is a runtime failure after the results were printed, not a usage
	// error: the help text must not follow it.
	cmd := &cobra.Command{}
	silenceUsageOnPricingTimeout(cmd, err)
	if !cmd.SilenceUsage {
		t.Error("a pricing timeout would be followed by the usage text")
	}

	o.timedOut = 0
	if err := reportPricing(o, 5*time.Minute); err != nil {
		t.Errorf("reportPricing without a timeout = %v, want nil (a missing published price is a note)", err)
	}
}

// TestPriceResults_EndToEndThroughClient runs the real batch path in
// pkg/aws (dedupe, concurrency, the injected pricer) behind priceResults,
// with a deadline that expires mid-run.
func TestPriceResults_EndToEndThroughClient(t *testing.T) {
	c := aws.NewClientFromConfig(sdkaws.Config{})
	c.SetOnDemandPricer(slowPricer{delay: 30 * time.Millisecond})
	var rs []aws.InstanceTypeResult
	for i := 0; i < 40; i++ {
		rs = append(rs, aws.InstanceTypeResult{InstanceType: fmt.Sprintf("m%d.large", i), Region: "us-west-2"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	o := priceResults(ctx, c, rs, nil, "ec2")
	if o.timedOut == 0 || o.timedOut == o.rows {
		t.Errorf("outcome = %+v, want some rows priced and the rest timed out", o)
	}
	if reportPricing(o, 100*time.Millisecond) == nil {
		t.Error("a deadline that cut pricing short did not produce an error")
	}
}

type slowPricer struct{ delay time.Duration }

func (s slowPricer) OnDemandPrice(ctx context.Context, _, _ string) (float64, error) {
	select {
	case <-time.After(s.delay):
		return 0.1, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

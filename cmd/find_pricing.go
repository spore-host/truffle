package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
)

// batchPricer is the part of *aws.Client that prices a result set.
type batchPricer interface {
	OnDemandPrices(ctx context.Context, keys []aws.PriceKey) map[aws.PriceKey]aws.PriceQuote
	SageMakerPrices(ctx context.Context, keys []aws.PriceKey) map[aws.PriceKey]aws.PriceQuote
}

// pricingOutcome counts how the on-demand price lookups for a result set went.
type pricingOutcome struct {
	rows     int // rows that needed a price
	static   int // priced from the built-in table, not the live Price List
	unpriced int // no price: AWS publishes none, or the lookup failed
	timedOut int // no price because the deadline expired (a subset of unpriced)
}

// priceResults fills OnDemandPrice for the rows of results whose instance type
// is in want (every row when want is nil). Lookups are deduped by (type,
// region) and run with bounded concurrency (truffle#210). Rows not wanted keep
// a zero price.
func priceResults(ctx context.Context, p batchPricer, results []aws.InstanceTypeResult, want map[string]bool, service string) pricingOutcome {
	var keys []aws.PriceKey
	var idx []int
	for i, r := range results {
		if want != nil && !want[r.InstanceType] {
			continue
		}
		keys = append(keys, aws.PriceKey{InstanceType: r.InstanceType, Region: r.Region})
		idx = append(idx, i)
	}
	out := pricingOutcome{rows: len(keys)}
	if len(keys) == 0 {
		return out
	}

	var quotes map[aws.PriceKey]aws.PriceQuote
	if service == "sagemaker" {
		quotes = p.SageMakerPrices(ctx, keys)
	} else {
		quotes = p.OnDemandPrices(ctx, keys)
	}
	for n, i := range idx {
		q := quotes[keys[n]]
		results[i].OnDemandPrice = q.Price
		switch {
		case q.Price > 0 && q.Source == aws.PriceSourceStatic:
			out.static++
		case q.Price <= 0:
			out.unpriced++
			if errors.Is(q.Err, context.DeadlineExceeded) || errors.Is(q.Err, context.Canceled) {
				out.timedOut++
			}
		}
	}
	return out
}

// reportPricing writes the stderr notes for a pricing outcome and returns the
// error find should exit with: non-nil only when the deadline cut pricing
// short, so a run that printed N/A for rows it never got to price does not exit
// 0 (truffle#210). Static-table prices and types AWS publishes no price for are
// notes, not errors.
func reportPricing(o pricingOutcome, timeout time.Duration) error {
	if o.static > 0 {
		fmt.Fprintf(os.Stderr, "%s On-demand price for %d of %d %s from truffle's built-in table, not the live AWS Price List — it may be out of date.\n",
			i18n.Symbol("warning"), o.static, o.rows, pluralize(o.static, "type is", "types are"))
	}
	if o.timedOut > 0 {
		return &pricingTimeoutError{fmt.Sprintf("on-demand pricing timed out after %s: no price for %d of %d %s (shown as N/A); increase --timeout or narrow --regions",
			timeout, o.timedOut, o.rows, pluralize(o.rows, "row", "rows"))}
	}
	if o.unpriced > 0 {
		fmt.Fprintf(os.Stderr, "Note: no on-demand price found for %d of %d %s (shown as N/A).\n",
			o.unpriced, o.rows, pluralize(o.rows, "row", "rows"))
	}
	return nil
}

// pricingTimeoutError is a run that printed its results but ran out of time
// pricing them. It is not a usage error, so the command's help is not shown.
type pricingTimeoutError struct{ msg string }

func (e *pricingTimeoutError) Error() string { return e.msg }

// silenceUsageOnPricingTimeout keeps cobra from following a pricing timeout
// with the usage text. Deferred by the commands that price results.
func silenceUsageOnPricingTimeout(cmd *cobra.Command, err error) {
	var pe *pricingTimeoutError
	if errors.As(err, &pe) {
		cmd.SilenceUsage = true
	}
}

// pickFirstPriceCandidates returns the instance types that can still come out
// on top once prices are known, for --pick-first: price only those, not the
// whole result set. It mirrors sortFindResults — every key it compares before
// price must tie with the best — and returns nil (price everything) when price
// is the primary key.
func pickFirstPriceCandidates(results []aws.InstanceTypeResult, f aws.FilterOptions, pref find.SortPreference) map[string]bool {
	if len(results) == 0 {
		return map[string]bool{}
	}
	const eps = 1e-9
	sizeSpec := hasSizeSpec(f)

	// prefix returns the keys sortFindResults compares before price, as
	// "lower is better" numbers.
	var prefix func(r aws.InstanceTypeResult) []float64
	switch pref {
	case find.SortCheapest, find.SortExpensive:
		return nil
	case find.SortPerformant:
		prefix = func(r aws.InstanceTypeResult) []float64 { return []float64{-float64(r.VCPUs)} }
	case find.SortNewest:
		prefix = func(r aws.InstanceTypeResult) []float64 {
			return []float64{-float64(instanceGeneration(r.InstanceType)), fitDistance(r, f)}
		}
	default:
		if sizeSpec {
			prefix = func(r aws.InstanceTypeResult) []float64 { return []float64{fitDistance(r, f)} }
		} else {
			prefix = func(r aws.InstanceTypeResult) []float64 {
				return []float64{-float64(instanceGeneration(r.InstanceType))}
			}
		}
	}

	// cmp orders prefix keys lexicographically, treating values within eps as
	// equal, the way cmpFit does.
	cmp := func(a, b []float64) int {
		for i := range a {
			switch {
			case a[i] < b[i]-eps:
				return -1
			case a[i] > b[i]+eps:
				return 1
			}
		}
		return 0
	}
	var best []float64
	for _, r := range results {
		if k := prefix(r); best == nil || cmp(k, best) < 0 {
			best = k
		}
	}
	want := make(map[string]bool)
	for _, r := range results {
		if cmp(prefix(r), best) == 0 {
			want[r.InstanceType] = true
		}
	}
	return want
}

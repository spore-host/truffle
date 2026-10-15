package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// PriceKey names one On-Demand price lookup: an instance type in a region.
type PriceKey struct {
	InstanceType string
	Region       string
}

// PriceQuote is the outcome of one lookup in a batch. Price is 0 when no rate
// was found, and Err then says why. Err wraps [context.DeadlineExceeded] or
// [context.Canceled] when the lookup was cut short, so a caller can tell "the
// run timed out" from "AWS publishes no price for this type".
type PriceQuote struct {
	Price  float64
	Source PriceSource
	Err    error
}

// PriceLookupConcurrency bounds how many Price List requests a batch lookup
// ([Client.OnDemandPrices], [Client.SageMakerPrices]) has in flight at once.
// The Price List API has a low rate limit (truffle#175, #182), so this stays
// small; the pricing client retries throttling with backoff on top of it.
const PriceLookupConcurrency = 4

// OnDemandPrices resolves the On-Demand $/hr for many (type, region) pairs at
// once, for callers such as `truffle find` that price a whole result set.
//
// Duplicate keys are looked up once. Lookups are grouped by instance type and
// run [PriceLookupConcurrency] types at a time. With the default pricer, a type
// wanted in more than one region costs one Price List request for every region
// rather than one per region, which is what makes a cross-region search
// affordable (truffle#210). An injected [OnDemandPricer] is called once per key.
//
// The result has an entry for every distinct key. When ctx ends partway
// through, the keys not yet priced carry the context error and no price; they
// are not filled from the static fallback table, because that would present a
// timeout as a merely stale price.
func (c *Client) OnDemandPrices(ctx context.Context, keys []PriceKey) map[PriceKey]PriceQuote {
	pricer := c.onDemandPricer()
	return runPriceBatch(ctx, keys, func(ctx context.Context, instanceType string, regions []string) map[string]PriceQuote {
		if bp, ok := pricer.(*fallbackPricer); ok {
			return bp.pricesForType(ctx, instanceType, regions)
		}
		out := make(map[string]PriceQuote, len(regions))
		for _, region := range regions {
			price, src, err := c.OnDemandPriceWithSource(ctx, instanceType, region)
			out[region] = quoteOrContextError(ctx, price, src, err)
		}
		return out
	})
}

// SageMakerPrices is [Client.OnDemandPrices] for SageMaker ml.* types: deduped
// and bounded the same way, one Price List request per (type, region).
func (c *Client) SageMakerPrices(ctx context.Context, keys []PriceKey) map[PriceKey]PriceQuote {
	return runPriceBatch(ctx, keys, func(ctx context.Context, instanceType string, regions []string) map[string]PriceQuote {
		out := make(map[string]PriceQuote, len(regions))
		for _, region := range regions {
			price, err := c.SageMakerPrice(ctx, instanceType, region)
			out[region] = quoteOrContextError(ctx, price, PriceSourceUnknown, err)
		}
		return out
	})
}

// quoteOrContextError builds a quote, reporting the context's error rather than
// the pricer's when the context has ended and no price came back: the pricer's
// error then is a symptom (a cancelled request), not the cause.
func quoteOrContextError(ctx context.Context, price float64, src PriceSource, err error) PriceQuote {
	if price > 0 {
		return PriceQuote{Price: price, Source: src}
	}
	if cerr := ctx.Err(); cerr != nil {
		return PriceQuote{Err: cerr}
	}
	if err == nil {
		err = errors.New("no on-demand price available")
	}
	return PriceQuote{Err: err}
}

// runPriceBatch dedupes keys, groups them by instance type, and calls price for
// each type with a bounded number of types in flight. Types that never started
// because ctx ended get the context error.
func runPriceBatch(ctx context.Context, keys []PriceKey, price func(ctx context.Context, instanceType string, regions []string) map[string]PriceQuote) map[PriceKey]PriceQuote {
	byType := make(map[string][]string)
	seen := make(map[PriceKey]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		byType[k.InstanceType] = append(byType[k.InstanceType], k.Region)
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
		sort.Strings(byType[t])
	}
	sort.Strings(types)

	out := make(map[PriceKey]PriceQuote, len(seen))
	var mu sync.Mutex
	sem := make(chan struct{}, PriceLookupConcurrency)
	var wg sync.WaitGroup
	for _, t := range types {
		regions := byType[t]
		acquired := false
		select {
		case sem <- struct{}{}:
			acquired = true
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			if acquired {
				<-sem
			}
			mu.Lock()
			for _, r := range regions {
				out[PriceKey{t, r}] = PriceQuote{Err: ctx.Err()}
			}
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(t string, regions []string) {
			defer wg.Done()
			defer func() { <-sem }()
			got := price(ctx, t, regions)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range regions {
				q, ok := got[r]
				if !ok {
					q = quoteOrContextError(ctx, 0, PriceSourceUnknown, fmt.Errorf("no on-demand price for %s in %s", t, r))
				}
				out[PriceKey{t, r}] = q
			}
		}(t, regions)
	}
	wg.Wait()
	return out
}

// pricesForType prices one instance type in several regions: the live Price
// List first (one request for all of them when there is more than one), then
// the static table for any region the live lookup could not price — unless the
// context has ended, in which case the region is left unpriced.
func (f *fallbackPricer) pricesForType(ctx context.Context, instanceType string, regions []string) map[string]PriceQuote {
	out := make(map[string]PriceQuote, len(regions))
	live := map[string]float64{}
	var liveErr error
	switch p := f.primary.(type) {
	case *awsOnDemandPricer:
		live, liveErr = p.pricesForType(ctx, instanceType, regions)
	case nil:
	default:
		for _, r := range regions {
			if price, err := p.OnDemandPrice(ctx, instanceType, r); err == nil && price > 0 {
				live[r] = price
			} else if err != nil {
				liveErr = err
			}
		}
	}
	for _, r := range regions {
		if price := live[r]; price > 0 {
			out[r] = PriceQuote{Price: price, Source: PriceSourceLive}
			continue
		}
		if cerr := ctx.Err(); cerr != nil {
			out[r] = PriceQuote{Err: cerr}
			continue
		}
		primaryErr := liveErr
		if primaryErr == nil {
			primaryErr = fmt.Errorf("no on-demand price found for %s in %s", instanceType, r)
		}
		if price, err := f.fallback.OnDemandPrice(ctx, instanceType, r); err == nil && price > 0 {
			out[r] = PriceQuote{Price: price, Source: PriceSourceStatic}
		} else {
			out[r] = PriceQuote{Err: primaryErr}
		}
	}
	return out
}

// pricesForType returns the live On-Demand rate for instanceType in each of
// regions it can price, from the cache where possible. One region is fetched
// with a region-filtered request; two or more with a single request that is
// not filtered by region, which returns the type's rate in every region.
// A region missing from the result has no published rate.
func (p *awsOnDemandPricer) pricesForType(ctx context.Context, instanceType string, regions []string) (map[string]float64, error) {
	out := make(map[string]float64, len(regions))
	var missing []string

	p.mu.Lock()
	for _, r := range regions {
		if c, ok := p.cache[instanceType+"\x00"+r]; ok && time.Since(c.fetched) < onDemandCacheTTL {
			out[r] = c.price
		} else {
			missing = append(missing, r)
		}
	}
	api := p.getProductsAPI()
	p.mu.Unlock()

	switch len(missing) {
	case 0:
		return out, nil
	case 1:
		price, err := fetchOnDemandPrice(ctx, api, instanceType, missing[0])
		if err != nil {
			return out, err
		}
		p.store(instanceType, missing[0], price)
		out[missing[0]] = price
		return out, nil
	}

	all, err := fetchOnDemandPricesAllRegions(ctx, api, instanceType)
	for r, price := range all {
		p.store(instanceType, r, price)
	}
	for _, r := range missing {
		if price, ok := all[r]; ok {
			out[r] = price
		}
	}
	return out, err
}

func (p *awsOnDemandPricer) store(instanceType, region string, price float64) {
	p.mu.Lock()
	p.cache[instanceType+"\x00"+region] = cachedPrice{price: price, fetched: time.Now()}
	p.mu.Unlock()
}

// getProductsAPI returns the Price List client, or the test seam when set.
// Callers hold p.mu.
func (p *awsOnDemandPricer) getProductsAPI() pricing.GetProductsAPIClient {
	if p.api != nil {
		return p.api
	}
	return p.ensureClient()
}

// fetchOnDemandPricesAllRegions queries the Price List for the Linux/shared
// On-Demand rate of one instance type in every region that offers it, keyed by
// regionCode. The filters match [fetchOnDemandPrice] minus the region, so each
// region's rate is the one a region-filtered request would return.
func fetchOnDemandPricesAllRegions(ctx context.Context, api pricing.GetProductsAPIClient, instanceType string) (map[string]float64, error) {
	pager := pricing.NewGetProductsPaginator(api, &pricing.GetProductsInput{
		ServiceCode:   aws.String("AmazonEC2"),
		FormatVersion: aws.String("aws_v1"),
		Filters:       onDemandFilters(instanceType, ""),
	})
	out := make(map[string]float64)
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return out, fmt.Errorf("price list GetProducts for %s: %w", instanceType, err)
		}
		for _, item := range page.PriceList {
			region := priceItemRegion(item)
			if region == "" {
				continue
			}
			if _, done := out[region]; done {
				continue
			}
			if price, ok := parseOnDemandFromPriceItem(item); ok {
				out[region] = price
			}
		}
	}
	return out, nil
}

// onDemandFilters is the Price List filter set for a Linux, shared-tenancy,
// no-preinstalled-software On-Demand rate. region "" omits the region filter.
func onDemandFilters(instanceType, region string) []pricingtypes.Filter {
	termMatch := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{
			Type:  pricingtypes.FilterTypeTermMatch,
			Field: aws.String(field),
			Value: aws.String(value),
		}
	}
	filters := []pricingtypes.Filter{termMatch("instanceType", instanceType)}
	if region != "" {
		filters = append(filters, termMatch("regionCode", region))
	}
	return append(filters,
		termMatch("operatingSystem", "Linux"),
		termMatch("tenancy", "Shared"),
		termMatch("preInstalledSw", "NA"),
		termMatch("capacitystatus", "Used"),
		termMatch("marketoption", "OnDemand"),
	)
}

// priceItemRegion returns product.attributes.regionCode from a Price List
// product document, or "" when it has none.
func priceItemRegion(item string) string {
	var doc struct {
		Product struct {
			Attributes struct {
				RegionCode string `json:"regionCode"`
			} `json:"attributes"`
		} `json:"product"`
	}
	if err := json.Unmarshal([]byte(item), &doc); err != nil {
		return ""
	}
	return doc.Product.Attributes.RegionCode
}

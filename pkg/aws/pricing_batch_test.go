package aws

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
)

// fakeGetProducts is a Price List stand-in. prices is type -> region -> $/hr.
// A request filtered by regionCode gets that region's product; one without
// gets every region's, split into pages of pageSize when pageSize > 0.
type fakeGetProducts struct {
	prices   map[string]map[string]float64
	pageSize int
	delay    time.Duration
	block    bool // wait for ctx to end, then fail with its error

	mu       sync.Mutex
	calls    int
	regional int // calls that carried a regionCode filter
	inFlight int32
	maxIn    int32
}

func priceDoc(instanceType, region string, price float64) string {
	return fmt.Sprintf(`{"product":{"attributes":{"instanceType":%q,"regionCode":%q}},`+
		`"terms":{"OnDemand":{"X":{"priceDimensions":{"Y":{"unit":"Hrs","pricePerUnit":{"USD":"%.4f"}}}}}}}`,
		instanceType, region, price)
}

func (f *fakeGetProducts) GetProducts(ctx context.Context, in *pricing.GetProductsInput, _ ...func(*pricing.Options)) (*pricing.GetProductsOutput, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	defer atomic.AddInt32(&f.inFlight, -1)
	for {
		m := atomic.LoadInt32(&f.maxIn)
		if n <= m || atomic.CompareAndSwapInt32(&f.maxIn, m, n) {
			break
		}
	}

	var instanceType, region string
	for _, flt := range in.Filters {
		switch aws.ToString(flt.Field) {
		case "instanceType":
			instanceType = aws.ToString(flt.Value)
		case "regionCode":
			region = aws.ToString(flt.Value)
		}
	}
	f.mu.Lock()
	f.calls++
	if region != "" {
		f.regional++
	}
	f.mu.Unlock()

	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var docs []string
	var names []string
	for r := range f.prices[instanceType] {
		names = append(names, r)
	}
	sort.Strings(names) // a stable order, so pages do not overlap
	for _, r := range names {
		if region == "" || r == region {
			docs = append(docs, priceDoc(instanceType, r, f.prices[instanceType][r]))
		}
	}
	if region != "" || f.pageSize <= 0 {
		return &pricing.GetProductsOutput{PriceList: docs}, nil
	}
	start := 0
	if in.NextToken != nil {
		_, _ = fmt.Sscanf(*in.NextToken, "%d", &start)
	}
	end := start + f.pageSize
	out := &pricing.GetProductsOutput{}
	if end < len(docs) {
		out.NextToken = aws.String(fmt.Sprint(end))
	} else {
		end = len(docs)
	}
	out.PriceList = docs[start:end]
	return out, nil
}

// clientWithFakePriceList is a Client whose default pricer (live Price List
// with the static fallback) talks to fake instead of AWS.
func clientWithFakePriceList(fake *fakeGetProducts) *Client {
	live := newAWSOnDemandPricer(aws.Config{Region: "us-east-1"})
	live.api = fake
	c := NewClientFromConfig(aws.Config{})
	c.SetOnDemandPricer(&fallbackPricer{primary: live, fallback: staticOnDemandPricer{}})
	return c
}

// TestOnDemandPrices_OneRequestPerTypeAcrossRegions is truffle#210: find used
// to make one GetProducts call per result row, so a cross-region search made
// (types x regions) calls and hit the 5-minute timeout. A type wanted in many
// regions must cost one request, and duplicate keys must cost nothing extra.
func TestOnDemandPrices_OneRequestPerTypeAcrossRegions(t *testing.T) {
	regions := []string{"us-east-1", "us-west-2", "eu-west-1", "ap-south-1"}
	fake := &fakeGetProducts{prices: map[string]map[string]float64{}}
	var keys []PriceKey
	for i, typ := range []string{"m8g.2xlarge", "c8g.4xlarge", "r8g.xlarge"} {
		fake.prices[typ] = map[string]float64{}
		for j, r := range regions {
			fake.prices[typ][r] = float64(i+1) + float64(j)/10
			keys = append(keys, PriceKey{typ, r}, PriceKey{typ, r}) // every key twice
		}
	}

	got := clientWithFakePriceList(fake).OnDemandPrices(context.Background(), keys)

	if fake.calls != 3 {
		t.Errorf("GetProducts calls = %d, want 3 (one per type, not one per row or per region)", fake.calls)
	}
	if len(got) != 12 {
		t.Errorf("got %d quotes, want 12 distinct keys", len(got))
	}
	for typ, byRegion := range fake.prices {
		for r, want := range byRegion {
			q := got[PriceKey{typ, r}]
			if q.Err != nil || math.Abs(q.Price-want) > 1e-9 || q.Source != PriceSourceLive {
				t.Errorf("%s %s = %+v, want live %.4f", typ, r, q, want)
			}
		}
	}
}

// TestOnDemandPrices_SingleRegionUsesRegionFilter: a type wanted in one region
// (the default, configured-region find) keeps the small region-filtered request
// rather than downloading every region's product.
func TestOnDemandPrices_SingleRegionUsesRegionFilter(t *testing.T) {
	fake := &fakeGetProducts{prices: map[string]map[string]float64{
		"m7a.4xlarge": {"us-west-2": 0.9274, "us-east-1": 0.9274},
		"c7a.4xlarge": {"us-west-2": 0.8211},
	}}
	got := clientWithFakePriceList(fake).OnDemandPrices(context.Background(), []PriceKey{
		{"m7a.4xlarge", "us-west-2"}, {"c7a.4xlarge", "us-west-2"},
	})
	if fake.calls != 2 || fake.regional != 2 {
		t.Errorf("calls = %d (regional %d), want 2 region-filtered calls", fake.calls, fake.regional)
	}
	if q := got[PriceKey{"m7a.4xlarge", "us-west-2"}]; q.Price != 0.9274 {
		t.Errorf("m7a.4xlarge = %+v, want 0.9274", q)
	}
}

// TestOnDemandPrices_BoundedConcurrency: lookups run in parallel, but never
// more than PriceLookupConcurrency at once — the Price List rate limit is low
// (#175, #182).
func TestOnDemandPrices_BoundedConcurrency(t *testing.T) {
	fake := &fakeGetProducts{prices: map[string]map[string]float64{}, delay: 20 * time.Millisecond}
	var keys []PriceKey
	for i := 0; i < 20; i++ {
		typ := fmt.Sprintf("m%d.large", i)
		fake.prices[typ] = map[string]float64{"us-west-2": 0.1}
		keys = append(keys, PriceKey{typ, "us-west-2"})
	}
	start := time.Now()
	clientWithFakePriceList(fake).OnDemandPrices(context.Background(), keys)
	elapsed := time.Since(start)

	if m := atomic.LoadInt32(&fake.maxIn); m < 2 || m > PriceLookupConcurrency {
		t.Errorf("max in-flight GetProducts = %d, want 2..%d", m, PriceLookupConcurrency)
	}
	if serial := 20 * fake.delay; elapsed >= serial {
		t.Errorf("20 lookups took %s, no faster than serial (%s)", elapsed, serial)
	}
}

// TestOnDemandPrices_TimeoutIsNotStale: when the deadline expires during
// pricing, the unpriced rows must say so. The old loop fell through to the
// static table, so a timed-out run reported its rows as merely "may be out of
// date" and exited 0 (#210). c7g.2xlarge/us-west-2 is in the static table, so
// a fallback would succeed here if it were tried.
func TestOnDemandPrices_TimeoutIsNotStale(t *testing.T) {
	fake := &fakeGetProducts{block: true}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	got := clientWithFakePriceList(fake).OnDemandPrices(ctx, []PriceKey{
		{"c7g.2xlarge", "us-west-2"}, {"c7g.2xlarge", "us-east-1"}, {"m7g.large", "us-west-2"},
	})
	for k, q := range got {
		if q.Price != 0 || q.Source == PriceSourceStatic {
			t.Errorf("%v = %+v after the deadline, want no price rather than a static one", k, q)
		}
		if !errors.Is(q.Err, context.DeadlineExceeded) {
			t.Errorf("%v err = %v, want context.DeadlineExceeded", k, q.Err)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d quotes, want 3", len(got))
	}
}

// TestOnDemandPrices_FallsBackWhenLiveHasNoRate: outside a timeout, a region
// the live Price List does not price still degrades to the static table, as
// the single-lookup path does (#114), and is labelled static.
func TestOnDemandPrices_FallsBackWhenLiveHasNoRate(t *testing.T) {
	fake := &fakeGetProducts{prices: map[string]map[string]float64{
		"c7g.2xlarge": {"eu-west-1": 0.3},
	}}
	got := clientWithFakePriceList(fake).OnDemandPrices(context.Background(), []PriceKey{
		{"c7g.2xlarge", "eu-west-1"}, {"c7g.2xlarge", "us-west-2"}, {"c7g.2xlarge", "xx-nowhere-1"},
	})
	if q := got[PriceKey{"c7g.2xlarge", "eu-west-1"}]; q.Source != PriceSourceLive {
		t.Errorf("eu-west-1 = %+v, want live", q)
	}
	if q := got[PriceKey{"c7g.2xlarge", "us-west-2"}]; q.Source != PriceSourceStatic || q.Price <= 0 {
		t.Errorf("us-west-2 = %+v, want a static-table price", q)
	}
	if q := got[PriceKey{"c7g.2xlarge", "xx-nowhere-1"}]; q.Price != 0 || q.Err == nil {
		t.Errorf("xx-nowhere-1 = %+v, want no price and an error", q)
	}
}

// TestOnDemandPrices_InjectedPricer: an embedder's pricer (no batch support)
// still gets one call per distinct key.
func TestOnDemandPrices_InjectedPricer(t *testing.T) {
	var calls int32
	c := NewClientFromConfig(aws.Config{})
	c.SetOnDemandPricer(countingPricer{price: 0.5, calls: &calls})
	got := c.OnDemandPrices(context.Background(), []PriceKey{
		{"a.large", "r1"}, {"a.large", "r1"}, {"a.large", "r2"}, {"b.large", "r1"},
	})
	if calls != 3 {
		t.Errorf("pricer calls = %d, want 3 distinct keys", calls)
	}
	if q := got[PriceKey{"a.large", "r2"}]; q.Price != 0.5 {
		t.Errorf("a.large r2 = %+v", q)
	}
}

type countingPricer struct {
	price float64
	calls *int32
}

func (p countingPricer) OnDemandPrice(context.Context, string, string) (float64, error) {
	atomic.AddInt32(p.calls, 1)
	return p.price, nil
}

// TestFetchOnDemandPricesAllRegions_Paginates: a type sold in many regions and
// Local Zones can span more than one Price List page.
func TestFetchOnDemandPricesAllRegions_Paginates(t *testing.T) {
	fake := &fakeGetProducts{prices: map[string]map[string]float64{"m5.large": {}}, pageSize: 2}
	for i := 0; i < 7; i++ {
		fake.prices["m5.large"][fmt.Sprintf("r-%d", i)] = 0.1
	}
	got, err := fetchOnDemandPricesAllRegions(context.Background(), fake, "m5.large")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 7 || fake.calls != 4 {
		t.Errorf("got %d regions in %d calls, want 7 in 4 pages", len(got), fake.calls)
	}
}

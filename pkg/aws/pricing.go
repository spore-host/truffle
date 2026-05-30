package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
	libpricing "github.com/spore-host/libs/pricing"
)

// OnDemandPricer resolves the On-Demand hourly rate ($/hr) for a Linux instance
// type in a region. Implementations should be safe for concurrent use.
//
// truffle uses this to populate [SpotPriceResult.OnDemandPrice] and
// [SpotPriceResult.SavingsPercent] (when [SpotOptions.ShowSavings] is set) and
// to back the [Client.HourlyRate] convenience. The default implementation
// ([NewAWSOnDemandPricer]) queries the AWS Price List API and caches results;
// callers embedding truffle as a library can inject their own via
// [Client.SetOnDemandPricer] (e.g. a fixture in tests).
type OnDemandPricer interface {
	// OnDemandPrice returns the On-Demand $/hr for instanceType in region.
	// It returns (0, error) when no price could be determined.
	OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error)
}

// SetOnDemandPricer overrides the On-Demand price source used by this client.
// Pass nil to reset to the default AWS Price List pricer. This is primarily for
// embedders and tests that want deterministic prices or an offline source.
func (c *Client) SetOnDemandPricer(p OnDemandPricer) {
	c.pricerOnce.Do(func() {}) // mark as initialized so the default is not installed later
	c.pricer = p
}

// onDemandPricer returns the active pricer, lazily installing the default
// AWS Price List pricer (with a static fallback) on first use.
func (c *Client) onDemandPricer() OnDemandPricer {
	c.pricerOnce.Do(func() {
		if c.pricer == nil {
			c.pricer = newDefaultOnDemandPricer(c.cfg)
		}
	})
	return c.pricer
}

// OnDemandPrice returns the current On-Demand $/hr for one instance type in one
// region (Linux, shared tenancy). It is a thin accessor over the client's
// [OnDemandPricer]; see [Client.HourlyRate] for a purchase-model-aware helper.
func (c *Client) OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error) {
	return c.onDemandPricer().OnDemandPrice(ctx, instanceType, region)
}

// HourlyRate returns the current $/hr for one instance type in one region under
// the given purchase model. It is a library-friendly convenience for embedders
// that want a single number rather than reducing a []SpotPriceResult by hand.
//
// model is case-insensitive:
//   - "on-demand" / "ondemand" / "" → the On-Demand rate.
//   - "spot" → the minimum current Spot price across AZs in the region (Spot
//     varies by AZ; the minimum is the best obtainable rate). Returns an error
//     if no Spot price is currently published for the type.
//
// "reserved" is not a point-in-time rate (it depends on term, payment option,
// and offering class) and is rejected; use the On-Demand rate as the baseline
// and apply your own reserved discount.
func (c *Client) HourlyRate(ctx context.Context, instanceType, region, model string) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "on-demand", "ondemand", "on_demand":
		price, err := c.OnDemandPrice(ctx, instanceType, region)
		if err != nil {
			return 0, err
		}
		if price <= 0 {
			return 0, fmt.Errorf("no on-demand price available for %s in %s", instanceType, region)
		}
		return price, nil

	case "spot":
		results, err := c.GetSpotPricing(ctx,
			[]InstanceTypeResult{{InstanceType: instanceType, Region: region}},
			SpotOptions{LookbackHours: 1})
		if err != nil {
			return 0, err
		}
		min := 0.0
		for _, r := range results {
			if r.SpotPrice > 0 && (min == 0 || r.SpotPrice < min) {
				min = r.SpotPrice
			}
		}
		if min == 0 {
			return 0, fmt.Errorf("no spot price available for %s in %s", instanceType, region)
		}
		return min, nil

	case "reserved":
		return 0, fmt.Errorf("reserved pricing is not a point-in-time rate; use the on-demand baseline and apply a reserved discount")

	default:
		return 0, fmt.Errorf("unknown purchase model %q (want \"on-demand\" or \"spot\")", model)
	}
}

// --- default pricer: AWS Price List API with a static fallback ---

// onDemandCacheTTL bounds how long a fetched On-Demand price is reused.
// On-Demand prices change rarely, so a long TTL keeps API calls minimal.
const onDemandCacheTTL = 24 * time.Hour

type cachedPrice struct {
	price   float64
	fetched time.Time
}

// awsOnDemandPricer resolves On-Demand prices via the AWS Price List
// (GetProducts) API, caching per (instanceType, region) with a TTL. The Price
// List API is only served from us-east-1 and ap-south-1, so it pins its own
// region regardless of the client's default region.
type awsOnDemandPricer struct {
	cfg aws.Config

	mu     sync.Mutex
	client *pricing.Client
	cache  map[string]cachedPrice
}

// newDefaultOnDemandPricer builds the production pricer: the AWS Price List API
// in front, falling back to the static libs/pricing table when the API is
// unavailable (e.g. no credentials, no network, or an emulator that does not
// implement the Price List endpoint).
func newDefaultOnDemandPricer(cfg aws.Config) OnDemandPricer {
	return &fallbackPricer{
		primary:  newAWSOnDemandPricer(cfg),
		fallback: staticOnDemandPricer{},
	}
}

// NewAWSOnDemandPricer returns an [OnDemandPricer] backed solely by the AWS
// Price List API (no static fallback). Most callers want the client default;
// this is exported for embedders that want to control fallback behavior.
func NewAWSOnDemandPricer(cfg aws.Config) OnDemandPricer {
	return newAWSOnDemandPricer(cfg)
}

func newAWSOnDemandPricer(cfg aws.Config) *awsOnDemandPricer {
	return &awsOnDemandPricer{cfg: cfg, cache: make(map[string]cachedPrice)}
}

func (p *awsOnDemandPricer) ensureClient() *pricing.Client {
	if p.client == nil {
		cfg := p.cfg
		// The Price List API has only two endpoints; us-east-1 is the canonical one.
		if cfg.Region == "" || (cfg.Region != "us-east-1" && cfg.Region != "ap-south-1") {
			cfg.Region = "us-east-1"
		}
		p.client = pricing.NewFromConfig(cfg)
	}
	return p.client
}

func (p *awsOnDemandPricer) OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error) {
	key := instanceType + "\x00" + region

	p.mu.Lock()
	if c, ok := p.cache[key]; ok && time.Since(c.fetched) < onDemandCacheTTL {
		price := c.price
		p.mu.Unlock()
		return price, nil
	}
	client := p.ensureClient()
	p.mu.Unlock()

	price, err := fetchOnDemandPrice(ctx, client, instanceType, region)
	if err != nil {
		return 0, err
	}

	p.mu.Lock()
	p.cache[key] = cachedPrice{price: price, fetched: time.Now()}
	p.mu.Unlock()
	return price, nil
}

// fetchOnDemandPrice queries the Price List API for the Linux/shared-tenancy
// On-Demand rate of one instance type in one region.
func fetchOnDemandPrice(ctx context.Context, client *pricing.Client, instanceType, region string) (float64, error) {
	termMatch := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{
			Type:  pricingtypes.FilterTypeTermMatch,
			Field: aws.String(field),
			Value: aws.String(value),
		}
	}

	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode:   aws.String("AmazonEC2"),
		FormatVersion: aws.String("aws_v1"),
		Filters: []pricingtypes.Filter{
			termMatch("instanceType", instanceType),
			termMatch("regionCode", region),
			termMatch("operatingSystem", "Linux"),
			termMatch("tenancy", "Shared"),
			termMatch("preInstalledSw", "NA"),
			termMatch("capacitystatus", "Used"),
			termMatch("marketoption", "OnDemand"),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("price list GetProducts for %s in %s: %w", instanceType, region, err)
	}

	for _, item := range out.PriceList {
		if price, ok := parseOnDemandFromPriceItem(item); ok {
			return price, nil
		}
	}
	return 0, fmt.Errorf("no on-demand price found for %s in %s", instanceType, region)
}

// parseOnDemandFromPriceItem extracts the USD per-hour On-Demand rate from a
// single Price List product JSON document. The structure is:
//
//	terms.OnDemand.<offerCode>.priceDimensions.<rateCode>.pricePerUnit.USD
func parseOnDemandFromPriceItem(item string) (float64, bool) {
	var doc struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					Unit         string            `json:"unit"`
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(item), &doc); err != nil {
		return 0, false
	}

	for _, offer := range doc.Terms.OnDemand {
		for _, dim := range offer.PriceDimensions {
			usd, ok := dim.PricePerUnit["USD"]
			if !ok {
				continue
			}
			price, err := strconv.ParseFloat(usd, 64)
			if err != nil || price <= 0 {
				continue // skip $0.00 placeholders that AWS sometimes emits
			}
			return price, true
		}
	}
	return 0, false
}

// staticOnDemandPricer resolves prices from the embedded libs/pricing table.
// It never errors: unknown types fall through to a family-based estimate.
type staticOnDemandPricer struct{}

func (staticOnDemandPricer) OnDemandPrice(_ context.Context, instanceType, region string) (float64, error) {
	return libpricing.GetEC2HourlyRate(region, instanceType), nil
}

// fallbackPricer tries primary first and falls back to fallback on error or a
// non-positive price, so a Price List outage degrades to the static estimate
// rather than producing zeroed savings.
type fallbackPricer struct {
	primary  OnDemandPricer
	fallback OnDemandPricer
}

func (f *fallbackPricer) OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error) {
	if f.primary != nil {
		if price, err := f.primary.OnDemandPrice(ctx, instanceType, region); err == nil && price > 0 {
			return price, nil
		}
	}
	return f.fallback.OnDemandPrice(ctx, instanceType, region)
}

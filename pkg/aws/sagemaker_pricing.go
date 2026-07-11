package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// SageMakerPricer resolves the On-Demand hourly rate ($/hr) for a SageMaker
// ml.* instance type in a region. SageMaker is priced under a distinct offer
// (AmazonSageMaker) with a management premium over the equivalent EC2 rate, so
// it cannot reuse the EC2 pricer. Implementations should be safe for concurrent
// use.
//
// The default implementation queries the AWS Price List API and caches results;
// embedders and tests can inject their own via [Client.SetSageMakerPricer].
type SageMakerPricer interface {
	// SageMakerPrice returns the On-Demand $/hr for an ml.* instanceType in
	// region. It returns (0, error) when no price could be determined.
	SageMakerPrice(ctx context.Context, instanceType, region string) (float64, error)
}

// sageMakerComputeComponents are the SageMaker usage components whose On-Demand
// rate truffle treats as the representative $/hr for an ml.* type. The Price
// List returns a row per component (Training, Hosting, Processing, Cluster,
// Notebook, Studio-*, ...); their compute rates are near-identical (verified
// live: they match to the cent), so any one is representative. truffle does NOT
// filter the query by component, because not every offered type carries every
// component (e.g. some types have no "Training" row but do have "Hosting") —
// filtering would drop a real, priced type to N/A. Instead it fetches all
// components and prefers a compute one from this set, falling back to any
// positive rate. Studio/notebook components (a few cents higher) are the last
// resort so the reported number reflects the compute rate when available.
var sageMakerComputeComponents = map[string]bool{
	"Training":       true,
	"Hosting":        true,
	"Processing":     true,
	"Cluster":        true,
	"BatchTransform": true,
	"AsyncInf":       true,
}

// SetSageMakerPricer overrides the SageMaker price source used by this client.
// Pass nil to reset to the default AWS Price List pricer. Primarily for
// embedders and tests that want deterministic prices or an offline source.
func (c *Client) SetSageMakerPricer(p SageMakerPricer) {
	c.smPricerOnce.Do(func() {}) // mark initialized so the default is not installed later
	c.smPricer = p
}

// sageMakerPricer returns the active pricer, lazily installing the default
// AWS Price List pricer on first use.
func (c *Client) sageMakerPricer() SageMakerPricer {
	c.smPricerOnce.Do(func() {
		if c.smPricer == nil {
			c.smPricer = newAWSSageMakerPricer(c.cfg)
		}
	})
	return c.smPricer
}

// SageMakerPrice returns the current On-Demand $/hr for one ml.* instance type
// in one region. It is a thin accessor over the client's [SageMakerPricer].
func (c *Client) SageMakerPrice(ctx context.Context, instanceType, region string) (float64, error) {
	return c.sageMakerPricer().SageMakerPrice(ctx, instanceType, region)
}

// awsSageMakerPricer resolves SageMaker On-Demand prices via the AWS Price List
// (GetProducts) API, caching per (instanceType, region) with a TTL. Like the
// EC2 pricer, it pins its own region since the Price List API is served only
// from us-east-1 and ap-south-1.
type awsSageMakerPricer struct {
	cfg aws.Config

	mu     sync.Mutex
	client *pricing.Client
	cache  map[string]cachedPrice
}

// NewAWSSageMakerPricer returns a [SageMakerPricer] backed by the AWS Price
// List API. Exported for embedders that want to control pricing directly.
func NewAWSSageMakerPricer(cfg aws.Config) SageMakerPricer {
	return newAWSSageMakerPricer(cfg)
}

func newAWSSageMakerPricer(cfg aws.Config) *awsSageMakerPricer {
	return &awsSageMakerPricer{cfg: cfg, cache: make(map[string]cachedPrice)}
}

func (p *awsSageMakerPricer) ensureClient() *pricing.Client {
	if p.client == nil {
		cfg := p.cfg
		if cfg.Region == "" || (cfg.Region != "us-east-1" && cfg.Region != "ap-south-1") {
			cfg.Region = "us-east-1"
		}
		p.client = pricing.NewFromConfig(cfg)
	}
	return p.client
}

func (p *awsSageMakerPricer) SageMakerPrice(ctx context.Context, instanceType, region string) (float64, error) {
	key := instanceType + "\x00" + region

	p.mu.Lock()
	if c, ok := p.cache[key]; ok && time.Since(c.fetched) < onDemandCacheTTL {
		price := c.price
		p.mu.Unlock()
		return price, nil
	}
	client := p.ensureClient()
	p.mu.Unlock()

	price, err := fetchSageMakerPrice(ctx, client, instanceType, region)
	if err != nil {
		return 0, err
	}

	p.mu.Lock()
	p.cache[key] = cachedPrice{price: price, fetched: time.Now()}
	p.mu.Unlock()
	return price, nil
}

// fetchSageMakerPrice queries the Price List API for the On-Demand rate of one
// ml.* instance type in one region. The instanceName attribute includes the
// "ml." prefix (verified live). It does not filter by component (see
// sageMakerComputeComponents); instead it fetches every component and returns a
// representative rate: a compute component when one exists, otherwise any
// positive rate (e.g. a Studio/notebook-only type).
func fetchSageMakerPrice(ctx context.Context, client *pricing.Client, instanceType, region string) (float64, error) {
	termMatch := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{
			Type:  pricingtypes.FilterTypeTermMatch,
			Field: aws.String(field),
			Value: aws.String(value),
		}
	}

	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode:   aws.String("AmazonSageMaker"),
		FormatVersion: aws.String("aws_v1"),
		Filters: []pricingtypes.Filter{
			termMatch("instanceName", instanceType),
			termMatch("regionCode", region),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("price list GetProducts for %s in %s: %w", instanceType, region, err)
	}

	return pickSageMakerRate(out.PriceList)
}

// pickSageMakerRate selects the representative On-Demand rate from a set of
// SageMaker Price List product documents (one per component). It prefers a
// compute component's rate; if none is present it falls back to any positive
// rate so a Studio/notebook-only type still reports a price rather than N/A.
func pickSageMakerRate(priceList []string) (float64, error) {
	var fallback float64
	for _, item := range priceList {
		price, ok := parseOnDemandFromPriceItem(item)
		if !ok {
			continue
		}
		if isSageMakerComputeComponent(item) {
			return price, nil
		}
		if fallback == 0 {
			fallback = price
		}
	}
	if fallback > 0 {
		return fallback, nil
	}
	return 0, fmt.Errorf("no SageMaker on-demand price found")
}

// isSageMakerComputeComponent reports whether a Price List product document is
// for one of the compute components (Training/Hosting/Processing/...).
func isSageMakerComputeComponent(item string) bool {
	var doc struct {
		Product struct {
			Attributes struct {
				Component string `json:"component"`
			} `json:"attributes"`
		} `json:"product"`
	}
	if err := json.Unmarshal([]byte(item), &doc); err != nil {
		return false
	}
	return sageMakerComputeComponents[doc.Product.Attributes.Component]
}

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

// SageMakerUsage names the SageMaker usage a price is being requested for. The
// Price List meters each usage as a separate component and their rates are not
// always equal (see sageMakerComputeComponents), so a caller pricing an
// inference endpoint should not silently receive the HyperPod rate (#107).
type SageMakerUsage string

const (
	// UsageDefault applies truffle's default preference (Hosting first), the
	// behaviour of a plain [Client.SageMakerPrice] call.
	UsageDefault SageMakerUsage = ""
	// UsageInference prices a real-time inference endpoint (Hosting component).
	UsageInference SageMakerUsage = "Hosting"
	// UsageTraining prices a training job (Training component).
	UsageTraining SageMakerUsage = "Training"
	// UsageProcessing prices a processing job (Processing component).
	UsageProcessing SageMakerUsage = "Processing"
	// UsageBatchTransform prices a batch transform job.
	UsageBatchTransform SageMakerUsage = "BatchTransform"
	// UsageAsyncInference prices an asynchronous inference endpoint.
	UsageAsyncInference SageMakerUsage = "AsyncInf"
	// UsageHyperPod prices a SageMaker HyperPod cluster (Cluster component) — a
	// distinct, typically higher rate than a plain endpoint.
	UsageHyperPod SageMakerUsage = "Cluster"
)

// SageMakerUsagePricer is an optional extension of [SageMakerPricer] for sources
// that can resolve a usage-specific rate. It is a separate interface rather than
// a second method on SageMakerPricer so existing embedder implementations keep
// compiling; [Client.SageMakerPriceFor] falls back to the plain SageMakerPrice
// when the active pricer does not implement it.
type SageMakerUsagePricer interface {
	SageMakerPricer
	// SageMakerPriceFor returns the On-Demand $/hr for a specific usage.
	SageMakerPriceFor(ctx context.Context, instanceType, region string, usage SageMakerUsage) (float64, error)
}

// sageMakerComputeComponents are the SageMaker usage components whose On-Demand
// rate truffle treats as the representative $/hr for an ml.* type. The Price
// List returns a row per component (Training, Hosting, Processing, Cluster,
// Notebook, Studio-*, ...). truffle does NOT filter the query by component,
// because not every offered type carries every component (e.g. some types have
// no "Training" row but do have "Hosting") — filtering would drop a real, priced
// type to N/A. Instead it fetches all components and prefers a compute one from
// this set, falling back to a Studio/notebook component (a few cents higher) only
// when a type offers no compute row at all, so the reported number reflects the
// compute rate when available.
//
// Membership alone is not enough to pick a rate: the compute rates are NOT
// always identical. An earlier version of this comment claimed they "match to
// the cent", which held for the ml.g5.* types it was verified against but no
// longer does — for ml.p4d.24xlarge (verified live 2026-07-27, us-east-1)
// Cluster is $25.910 while Hosting/Training are $25.251, a 2.6% spread, because
// Cluster is SageMaker HyperPod rather than a plain endpoint. See #107. Use
// sageMakerComponentPreference to choose deterministically.
var sageMakerComputeComponents = map[string]bool{
	"Training":       true,
	"Hosting":        true,
	"Processing":     true,
	"Cluster":        true,
	"BatchTransform": true,
	"AsyncInf":       true,
}

// sageMakerComponentPreference orders the compute components from most to least
// representative of "what an ml.* instance costs to run". It exists so the rate
// does not depend on Price List response ordering (#107): before this, whichever
// accepted component AWS happened to return first won, so the same type could
// report the HyperPod rate on one call and the Hosting rate on the next.
//
// Hosting leads because a real-time inference endpoint is the most common thing
// a caller is pricing, and Training/Processing/BatchTransform/AsyncInf sit at the
// same rate in every case measured so far. Cluster is deliberately LAST among
// compute components: it meters SageMaker HyperPod, a distinct (higher) product,
// so it should only be reported when a type offers nothing else.
var sageMakerComponentPreference = []string{
	"Hosting",
	"Training",
	"Processing",
	"BatchTransform",
	"AsyncInf",
	"Cluster",
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
// in one region. It is a thin accessor over the client's [SageMakerPricer], and
// uses truffle's default component preference (Hosting first).
func (c *Client) SageMakerPrice(ctx context.Context, instanceType, region string) (float64, error) {
	return c.sageMakerPricer().SageMakerPrice(ctx, instanceType, region)
}

// SageMakerPriceFor returns the On-Demand $/hr for a specific SageMaker usage —
// e.g. [UsageInference] for a real-time endpoint or [UsageHyperPod] for a
// cluster. Prefer it over [Client.SageMakerPrice] when the caller knows what it
// is pricing, since the per-component rates differ for some types (#107).
//
// When the active pricer is a plain [SageMakerPricer] (an embedder's custom
// implementation, or a test fake) the usage cannot be honoured and this falls
// back to SageMakerPrice, so the call always returns a usable rate.
func (c *Client) SageMakerPriceFor(ctx context.Context, instanceType, region string, usage SageMakerUsage) (float64, error) {
	pricer := c.sageMakerPricer()
	if up, ok := pricer.(SageMakerUsagePricer); ok {
		return up.SageMakerPriceFor(ctx, instanceType, region, usage)
	}
	return pricer.SageMakerPrice(ctx, instanceType, region)
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
	return p.SageMakerPriceFor(ctx, instanceType, region, UsageDefault)
}

// SageMakerPriceFor implements [SageMakerUsagePricer]. The usage is part of the
// cache key: different usages can resolve to different rates for the same
// (type, region), so sharing one entry would let an inference lookup return a
// cached HyperPod rate (#107).
func (p *awsSageMakerPricer) SageMakerPriceFor(ctx context.Context, instanceType, region string, usage SageMakerUsage) (float64, error) {
	key := instanceType + "\x00" + region + "\x00" + string(usage)

	p.mu.Lock()
	if c, ok := p.cache[key]; ok && time.Since(c.fetched) < onDemandCacheTTL {
		price := c.price
		p.mu.Unlock()
		return price, nil
	}
	client := p.ensureClient()
	p.mu.Unlock()

	price, err := fetchSageMakerPrice(ctx, client, instanceType, region, usage)
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
//
// usage, when not [UsageDefault], puts that component first in the preference
// order so the returned rate matches what the caller is actually pricing (#107).
func fetchSageMakerPrice(ctx context.Context, client *pricing.Client, instanceType, region string, usage SageMakerUsage) (float64, error) {
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

	if usage == UsageDefault {
		return pickSageMakerRate(out.PriceList)
	}
	return pickSageMakerRateFor(out.PriceList, string(usage))
}

// pickSageMakerRate selects the representative On-Demand rate from a set of
// SageMaker Price List product documents (one per component), using the default
// preference order (Hosting first — see sageMakerComponentPreference). It is
// equivalent to pickSageMakerRateFor(priceList) with no explicit preference.
func pickSageMakerRate(priceList []string) (float64, error) {
	return pickSageMakerRateFor(priceList)
}

// pickSageMakerRateFor selects the On-Demand rate for the caller's intended
// usage. prefer lists component names in descending priority — e.g. pass
// "Training" when pricing a training job, or "Cluster" when pricing HyperPod.
// Any components not named fall back to the default order, so an incomplete
// preference still yields a deterministic answer.
//
// Selection is by *preference*, not by response order (#107): the whole price
// list is scanned and the best-ranked component present wins, so the result is
// stable regardless of how AWS orders the response. If no compute component is
// present the highest-ranked non-compute row (Studio/notebook) is used, so a
// notebook-only type still reports a price rather than N/A.
//
// Rows carrying no component attribute are never selected. That is what keeps
// the USE1-TrainingPlanUpfrontFee row out of the result: it is a one-off
// reservation fee, not an hourly rate, and it is *lower* than the real hourly
// rate ($13.57 vs $25.25 for ml.p4d.24xlarge), so reporting it would make
// SageMaker look ~38% cheaper than the equivalent EC2 instance. An unpriced type
// is the honest answer (#107).
func pickSageMakerRateFor(priceList []string, prefer ...string) (float64, error) {
	// rank maps a component name to its position in the effective preference
	// order; lower is better. Explicit preferences come first, then the defaults.
	rank := make(map[string]int, len(prefer)+len(sageMakerComponentPreference))
	for _, c := range append(append([]string{}, prefer...), sageMakerComponentPreference...) {
		if _, seen := rank[c]; !seen {
			rank[c] = len(rank)
		}
	}

	const noRank = -1
	bestRank, bestPrice := noRank, 0.0
	fallbackRank, fallbackPrice := noRank, 0.0

	for _, item := range priceList {
		price, ok := parseOnDemandFromPriceItem(item)
		if !ok {
			continue
		}
		component := sageMakerComponent(item)

		// No component attribute → not a per-hour instance rate. This is the
		// upfront-fee guard described above; skip it outright rather than letting it
		// become the non-compute fallback.
		if component == "" {
			continue
		}

		if sageMakerComputeComponents[component] {
			r, known := rank[component]
			if !known {
				r = len(rank) // a compute component with no explicit rank sorts last
			}
			if bestRank == noRank || r < bestRank {
				bestRank, bestPrice = r, price
			}
			continue
		}

		// Non-compute (Studio/notebook/...) — only used if no compute row exists.
		// Rank these too so the fallback is order-independent as well.
		if r, known := rank[component]; known {
			if fallbackRank == noRank || r < fallbackRank {
				fallbackRank, fallbackPrice = r, price
			}
		} else if fallbackRank == noRank {
			fallbackRank, fallbackPrice = len(rank), price
		}
	}

	if bestRank != noRank {
		return bestPrice, nil
	}
	if fallbackPrice > 0 {
		return fallbackPrice, nil
	}
	return 0, fmt.Errorf("no SageMaker on-demand price found")
}

// sageMakerComponent extracts the Price List "component" product attribute, or
// "" when the document has none. A missing component is how truffle avoids the
// USE1-TrainingPlanUpfrontFee row — a one-off reservation fee, not an hourly
// rate — which is cheaper than the real hourly rate and would make SageMaker
// look 38% cheaper than the equivalent EC2 instance if it were ever selected.
func sageMakerComponent(item string) string {
	var doc struct {
		Product struct {
			Attributes struct {
				Component string `json:"component"`
			} `json:"attributes"`
		} `json:"product"`
	}
	if err := json.Unmarshal([]byte(item), &doc); err != nil {
		return ""
	}
	return doc.Product.Attributes.Component
}

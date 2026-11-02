package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
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

// PriceSource says where an On-Demand rate came from, so a caller that spends
// money on the answer can decide whether to trust it.
type PriceSource string

const (
	// PriceSourceUnknown means the pricer did not report a source. This is what
	// an injected [OnDemandPricer] returns unless it implements
	// [SourcedOnDemandPricer].
	PriceSourceUnknown PriceSource = ""

	// PriceSourceLive means the rate came from the AWS Price List API — the
	// current, authoritative rate.
	PriceSourceLive PriceSource = "live"

	// PriceSourceStatic means the rate came from truffle's embedded table because
	// the Price List API was unavailable. It is a real published rate that was
	// correct when the table was last refreshed, but it may be stale and it
	// covers only common types in eight regions. Do not gate spending on it
	// without saying so.
	PriceSourceStatic PriceSource = "static"
)

// PriceOrigin is a price's provenance: where it came from and, for a static
// price, WHY the live Price List was not used and WHEN the static data was
// collected. Every price truffle prints or serializes carries one, embedded in
// the result type, so JSON/YAML gain price_source, price_as_of and
// price_fallback_reason next to the price itself.
//
// The owner rule it implements (truffle#184): a static or fallback price must
// say that it is static, why, and when it was collected — a stale rate that
// reads like a current one is the #114 failure ("a fabricated price is worse
// than no price") with a slower fuse. Build a static origin only with
// [StaticPriceOrigin], which fills all three; a gate rejects any other
// construction.
type PriceOrigin struct {
	// PriceSource is live, static, or unknown ("") when no price was resolved
	// or the pricer could not say.
	PriceSource PriceSource `json:"price_source,omitempty" yaml:"price_source,omitempty"`
	// PriceAsOf is the date (YYYY-MM-DD) the static rates were collected; set
	// only for a static price. Empty for an injected static pricer that did not
	// report one, which renders as "collection date unknown" rather than
	// pretending.
	PriceAsOf string `json:"price_as_of,omitempty" yaml:"price_as_of,omitempty"`
	// PriceFallbackReason says why the live Price List was not used (its error,
	// or that it returned no price); set only for a static price.
	PriceFallbackReason string `json:"price_fallback_reason,omitempty" yaml:"price_fallback_reason,omitempty"`
}

// LivePriceOrigin is the origin of a rate read from the live AWS Price List
// (including one cached from it within this process).
func LivePriceOrigin() PriceOrigin { return PriceOrigin{PriceSource: PriceSourceLive} }

// StaticPriceOrigin is the origin of a rate from truffle's embedded table
// (libs/pricing): static, collected at [libpricing.PricesAsOf], for reason.
// It is the only constructor of a static origin, so the date and the reason
// can't be left off. An empty reason is recorded as "reason not recorded"
// rather than left blank.
func StaticPriceOrigin(reason string) PriceOrigin {
	return staticOrigin(libpricing.PricesAsOf, reason)
}

// staticOrigin builds a static origin with an explicit collection date (empty
// when the source of the rate could not say).
func staticOrigin(asOf, reason string) PriceOrigin {
	if strings.TrimSpace(reason) == "" {
		reason = "reason not recorded"
	}
	return PriceOrigin{PriceSource: PriceSourceStatic, PriceAsOf: asOf, PriceFallbackReason: reason}
}

// liveUnavailableReason words why a static rate was used in place of the live
// Price List: its error, or (with no error) that it had no rate for the pair.
func liveUnavailableReason(err error, instanceType, region string) string {
	if err == nil {
		return fmt.Sprintf("the live AWS Price List returned no on-demand price for %s in %s", instanceType, region)
	}
	return fmt.Sprintf("live AWS Price List unavailable: %v", err)
}

// IsStatic reports whether the price came from a static table.
func (o PriceOrigin) IsStatic() bool { return o.PriceSource == PriceSourceStatic }

// Label is the one wording of a price's origin, shared by every renderer so
// the phrasing can't drift between commands:
//
//	live
//	static, collected 2026-10-07: live AWS Price List unavailable: <err>
//
// and "" for an unknown source.
func (o PriceOrigin) Label() string {
	switch o.PriceSource {
	case PriceSourceLive:
		return "live"
	case PriceSourceStatic:
		date := "collection date unknown"
		if o.PriceAsOf != "" {
			date = "collected " + o.PriceAsOf
		}
		reason := o.PriceFallbackReason
		if reason == "" {
			reason = "reason not recorded"
		}
		return fmt.Sprintf("static, %s: %s", date, reason)
	default:
		return ""
	}
}

// DescribeOnDemandPrice renders an On-Demand rate with its origin, e.g.
//
//	$0.7140/hr on-demand (live)
//	$0.7140/hr on-demand (static, collected 2026-10-07: live AWS Price List unavailable: <err>)
func DescribeOnDemandPrice(price float64, o PriceOrigin) string {
	s := fmt.Sprintf("$%.4f/hr on-demand", price)
	if label := o.Label(); label != "" {
		s += " (" + label + ")"
	}
	return s
}

// StaticPriceNotes returns the distinct origin labels among the static origins
// given, sorted, for a table that marks static cells and explains them once in
// a footer. Nil when none is static.
func StaticPriceNotes(origins []PriceOrigin) []string {
	seen := map[string]bool{}
	var notes []string
	for _, o := range origins {
		if !o.IsStatic() {
			continue
		}
		if l := o.Label(); !seen[l] {
			seen[l] = true
			notes = append(notes, l)
		}
	}
	sort.Strings(notes)
	return notes
}

// OriginOnDemandPricer is an optional extension of [OnDemandPricer] for
// implementations that can report a rate's full [PriceOrigin]. truffle's
// default pricer implements it; [Client.OnDemandPriceWithOrigin] uses it when
// available.
type OriginOnDemandPricer interface {
	OnDemandPricer

	// OnDemandPriceWithOrigin returns the rate and its provenance.
	OnDemandPriceWithOrigin(ctx context.Context, instanceType, region string) (float64, PriceOrigin, error)
}

// SourcedOnDemandPricer is an optional extension of [OnDemandPricer] for
// implementations that can report where a rate came from. truffle's default
// pricer implements it; [Client.OnDemandPriceWithSource] uses it when available
// and reports [PriceSourceUnknown] otherwise.
type SourcedOnDemandPricer interface {
	OnDemandPricer

	// OnDemandPriceWithSource returns the rate and its provenance.
	OnDemandPriceWithSource(ctx context.Context, instanceType, region string) (float64, PriceSource, error)
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

// OnDemandPriceWithSource is [Client.OnDemandPrice] plus the rate's provenance,
// for callers that spend money on the answer and need to know whether it is the
// live Price List rate or truffle's embedded fallback table.
//
// The source is [PriceSourceUnknown] when the active pricer does not implement
// [SourcedOnDemandPricer] — which is the case for an injected pricer that only
// satisfies [OnDemandPricer].
func (c *Client) OnDemandPriceWithSource(ctx context.Context, instanceType, region string) (float64, PriceSource, error) {
	price, origin, err := c.OnDemandPriceWithOrigin(ctx, instanceType, region)
	return price, origin.PriceSource, err
}

// OnDemandPriceWithOrigin is [Client.OnDemandPrice] plus the rate's full
// [PriceOrigin]: for a static rate, why the live Price List was not used and
// when the static table was collected (#184). Use it for anything a user will
// see.
//
// An injected pricer that implements only [SourcedOnDemandPricer] gets its
// source passed through; a static rate from it is labelled with an unknown
// collection date and "reported by the injected pricer", since truffle can't
// vouch for either. A plain [OnDemandPricer] reports [PriceSourceUnknown].
func (c *Client) OnDemandPriceWithOrigin(ctx context.Context, instanceType, region string) (float64, PriceOrigin, error) {
	p := c.onDemandPricer()
	if op, ok := p.(OriginOnDemandPricer); ok {
		return op.OnDemandPriceWithOrigin(ctx, instanceType, region)
	}
	if sp, ok := p.(SourcedOnDemandPricer); ok {
		price, src, err := sp.OnDemandPriceWithSource(ctx, instanceType, region)
		return price, originFromSource(src), err
	}
	price, err := p.OnDemandPrice(ctx, instanceType, region)
	return price, PriceOrigin{}, err
}

// originFromSource is the best origin for a pricer that reports only a
// source. A static source from such a pricer has no date or reason truffle
// can vouch for, so it says so rather than borrowing libs/pricing's date.
func originFromSource(src PriceSource) PriceOrigin {
	switch src {
	case PriceSourceLive:
		return LivePriceOrigin()
	case PriceSourceStatic:
		return staticOrigin("", "reported by the injected pricer")
	default:
		return PriceOrigin{PriceSource: src}
	}
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

	// api, when set, replaces the Price List client (a test seam).
	api pricing.GetProductsAPIClient
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

// pricingMaxAttempts is the retry budget for GetProducts.
//
// The SDK default is 3, which is not enough for this API under fan-out
// (truffle#175). The Price List API has a notoriously low rate limit, and
// `spawn task run` is ONE PROCESS PER LAUNCH — so eight concurrent launches are
// eight cold caches making eight GetProducts calls at once. The 24-hour cache
// above is the right design for a long-lived process and can never amortise the
// case that actually throttles.
//
// A refused launch is the consequence: with --cost-limit set, truffle fails
// closed when no price can be resolved (#114, correctly), so one ThrottlingException
// refuses the launch outright.
//
// 8 attempts with the SDK's exponential backoff and jitter spans tens of seconds,
// which is cheap against a launch that is about to run for minutes and far cheaper
// than the alternative of substituting a guessed price into a spend cap.
const pricingMaxAttempts = 8

func (p *awsOnDemandPricer) ensureClient() *pricing.Client {
	if p.client == nil {
		p.client = newPriceListClient(p.cfg)
	}
	return p.client
}

// newPriceListClient builds the Price List API client both pricers use (EC2
// on-demand and SageMaker), so they share one endpoint choice and one retry
// policy (#219).
//
// The Price List API has only two endpoints; us-east-1 is the canonical one,
// and it prices every region. Throttling is retried harder than the SDK
// default (truffle#175): retry.NewStandard already treats throttling as
// retryable and applies exponential backoff with jitter, but the default
// attempt budget of 3 is too small for a concurrently-launched fan-out, or for
// find pricing PriceLookupConcurrency types at once. The retryer is set on the
// config before NewFromConfig copies it.
func newPriceListClient(cfg aws.Config) *pricing.Client {
	if cfg.Region == "" || (cfg.Region != "us-east-1" && cfg.Region != "ap-south-1") {
		cfg.Region = "us-east-1"
	}
	cfg.Retryer = func() aws.Retryer {
		return retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = pricingMaxAttempts
		})
	}
	return pricing.NewFromConfig(cfg)
}

func (p *awsOnDemandPricer) OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error) {
	key := instanceType + "\x00" + region

	p.mu.Lock()
	if c, ok := p.cache[key]; ok && time.Since(c.fetched) < onDemandCacheTTL {
		price := c.price
		p.mu.Unlock()
		return price, nil
	}
	api := p.getProductsAPI()
	p.mu.Unlock()

	price, err := fetchOnDemandPrice(ctx, api, instanceType, region)
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
func fetchOnDemandPrice(ctx context.Context, api pricing.GetProductsAPIClient, instanceType, region string) (float64, error) {
	out, err := api.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode:   aws.String("AmazonEC2"),
		FormatVersion: aws.String("aws_v1"),
		Filters:       onDemandFilters(instanceType, region),
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

// staticOnDemandPricer resolves prices from the embedded libs/pricing table by
// exact (region, instanceType) lookup, and errors when the table has no such
// entry.
//
// It deliberately does NOT call [libpricing.GetEC2HourlyRate], which never
// errors: that helper substitutes us-east-1 prices for an unknown region and
// then falls through to a family-based guess for an unknown type (unknown family
// 0.10 × unknown size 2.0 = $0.20/hr). Both substitutions are invisible to the
// caller, so hpc7a.96xlarge in a region that does not offer it came back as
// $0.20/hr against a real $7.20 (#114). A degraded price is defensible; a
// fabricated one is not, because nothing downstream can tell the difference.
type staticOnDemandPricer struct{}

func (staticOnDemandPricer) OnDemandPrice(_ context.Context, instanceType, region string) (float64, error) {
	key := strings.ToLower(strings.TrimSpace(instanceType))
	regionKey := strings.ToLower(strings.TrimSpace(region))

	prices, ok := libpricing.EC2Pricing[regionKey]
	if !ok {
		return 0, fmt.Errorf("no static price table for region %s", region)
	}
	price, ok := prices[key]
	if !ok || price <= 0 {
		return 0, fmt.Errorf("no static price for %s in %s", instanceType, region)
	}
	return price, nil
}

// fallbackPricer tries primary first and falls back to fallback on error or a
// non-positive price, so a Price List outage (no credentials, no network, or an
// emulator that does not implement the endpoint) degrades to the static table
// rather than producing zeroed savings.
//
// The fallback can itself fail — see [staticOnDemandPricer] — and when it does,
// the primary's error is what a caller needs, because it says why the live
// lookup failed. So the primary error is preserved and returned, with the
// fallback's attached for context.
type fallbackPricer struct {
	primary  OnDemandPricer
	fallback OnDemandPricer
}

func (f *fallbackPricer) OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error) {
	price, _, err := f.OnDemandPriceWithSource(ctx, instanceType, region)
	return price, err
}

func (f *fallbackPricer) OnDemandPriceWithSource(ctx context.Context, instanceType, region string) (float64, PriceSource, error) {
	price, origin, err := f.OnDemandPriceWithOrigin(ctx, instanceType, region)
	return price, origin.PriceSource, err
}

// OnDemandPriceWithOrigin keeps the primary's error on the SUCCESSFUL fallback
// too: it is the "why" a static rate must carry (#184). Before, it survived
// only when the fallback also failed, so no command could say why a printed
// static price was static.
func (f *fallbackPricer) OnDemandPriceWithOrigin(ctx context.Context, instanceType, region string) (float64, PriceOrigin, error) {
	var primaryErr error
	reason := "live AWS Price List not consulted"
	if f.primary != nil {
		price, err := f.primary.OnDemandPrice(ctx, instanceType, region)
		if err == nil && price > 0 {
			return price, LivePriceOrigin(), nil
		}
		reason = liveUnavailableReason(err, instanceType, region)
		primaryErr = err
		if primaryErr == nil {
			primaryErr = fmt.Errorf("no on-demand price for %s in %s", instanceType, region)
		}
	}

	price, err := f.fallback.OnDemandPrice(ctx, instanceType, region)
	if err == nil && price > 0 {
		return price, StaticPriceOrigin(reason), nil
	}
	if primaryErr == nil {
		return 0, PriceOrigin{}, err
	}
	if err == nil {
		return 0, PriceOrigin{}, primaryErr
	}
	return 0, PriceOrigin{}, fmt.Errorf("%w (static fallback also unavailable: %v)", primaryErr, err)
}

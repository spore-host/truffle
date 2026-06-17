// Package awsmock provides a configurable mock implementation of [aws.Finder]
// for unit testing downstream consumers of truffle's instance discovery API.
//
// Basic usage:
//
//	m := awsmock.New(
//	    awsmock.WithInstances([]aws.InstanceTypeResult{
//	        {InstanceType: "m7i.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8192},
//	    }),
//	    awsmock.WithRegions([]string{"us-east-1", "us-west-2"}),
//	    awsmock.WithOnDemandPrices(map[string]float64{"m7i.large/us-east-1": 0.1008}),
//	)
//	// Use m anywhere an aws.Finder is accepted.
package awsmock

import (
	"context"
	"fmt"
	"regexp"

	"github.com/spore-host/truffle/pkg/aws"
)

// Finder is a mock implementation of [aws.Finder]. Configure it with
// [New] and option functions, or set fields directly for simple cases.
type Finder struct {
	Regions        []string
	Instances      []aws.InstanceTypeResult
	SpotPrices     []aws.SpotPriceResult
	Reservations   []aws.CapacityReservationResult
	Blocks         []aws.CapacityBlockResult
	BlockOfferings []aws.CapacityBlockOfferingResult
	OnDemandMap    map[string]float64  // key: "instanceType/region"
	InstanceTypes  map[string][]string // key: region

	// Error injection: if non-nil, the corresponding method returns this error.
	SearchErr         error
	SpotErr           error
	RegionsErr        error
	ReservationErr    error
	BlocksErr         error
	BlockOfferingsErr error
	PriceErr          error
}

// compile-time check
var _ aws.Finder = (*Finder)(nil)

// Option configures a [Finder].
type Option func(*Finder)

// New creates a mock Finder with the given options.
func New(opts ...Option) *Finder {
	m := &Finder{
		Regions:     []string{"us-east-1"},
		OnDemandMap: make(map[string]float64),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithRegions sets the regions returned by GetEnabledRegions and GetAllRegions.
func WithRegions(regions []string) Option {
	return func(m *Finder) { m.Regions = regions }
}

// WithInstances sets the instance results returned by SearchInstanceTypes.
func WithInstances(instances []aws.InstanceTypeResult) Option {
	return func(m *Finder) { m.Instances = instances }
}

// WithSpotPrices sets the results returned by GetSpotPricing.
func WithSpotPrices(prices []aws.SpotPriceResult) Option {
	return func(m *Finder) { m.SpotPrices = prices }
}

// WithOnDemandPrices sets per-type/region prices. Keys are "instanceType/region".
func WithOnDemandPrices(prices map[string]float64) Option {
	return func(m *Finder) { m.OnDemandMap = prices }
}

// WithReservations sets the ODCRs returned by GetCapacityReservations.
func WithReservations(reservations []aws.CapacityReservationResult) Option {
	return func(m *Finder) { m.Reservations = reservations }
}

// WithBlocks sets the Capacity Blocks returned by GetCapacityBlocks.
func WithBlocks(blocks []aws.CapacityBlockResult) Option {
	return func(m *Finder) { m.Blocks = blocks }
}

// WithBlockOfferings sets the offerings returned by GetCapacityBlockOfferings.
func WithBlockOfferings(offerings []aws.CapacityBlockOfferingResult) Option {
	return func(m *Finder) { m.BlockOfferings = offerings }
}

// WithError injects an error into all methods.
func WithError(err error) Option {
	return func(m *Finder) {
		m.SearchErr = err
		m.SpotErr = err
		m.RegionsErr = err
		m.ReservationErr = err
		m.BlocksErr = err
		m.PriceErr = err
	}
}

func (m *Finder) GetEnabledRegions(_ context.Context) ([]string, error) {
	if m.RegionsErr != nil {
		return nil, m.RegionsErr
	}
	return m.Regions, nil
}

func (m *Finder) GetAllRegions(_ context.Context) ([]string, error) {
	if m.RegionsErr != nil {
		return nil, m.RegionsErr
	}
	return m.Regions, nil
}

func (m *Finder) GetInstanceTypes(_ context.Context, region string) ([]string, error) {
	if m.SearchErr != nil {
		return nil, m.SearchErr
	}
	if m.InstanceTypes != nil {
		return m.InstanceTypes[region], nil
	}
	// Derive from Instances
	var types []string
	seen := make(map[string]bool)
	for _, inst := range m.Instances {
		if inst.Region == region && !seen[inst.InstanceType] {
			seen[inst.InstanceType] = true
			types = append(types, inst.InstanceType)
		}
	}
	return types, nil
}

func (m *Finder) SearchInstanceTypes(_ context.Context, regions []string, matcher *regexp.Regexp, opts aws.FilterOptions) ([]aws.InstanceTypeResult, error) {
	if m.SearchErr != nil {
		return nil, m.SearchErr
	}
	regionSet := make(map[string]bool, len(regions))
	for _, r := range regions {
		regionSet[r] = true
	}
	var results []aws.InstanceTypeResult
	for _, inst := range m.Instances {
		if !regionSet[inst.Region] {
			continue
		}
		if matcher != nil && !matcher.MatchString(inst.InstanceType) {
			continue
		}
		if opts.Architecture != "" && inst.Architecture != opts.Architecture {
			continue
		}
		if opts.MinVCPUs > 0 && int(inst.VCPUs) < opts.MinVCPUs {
			continue
		}
		if opts.MinMemory > 0 && float64(inst.MemoryMiB)/1024.0 < opts.MinMemory {
			continue
		}
		results = append(results, inst)
	}
	return results, nil
}

func (m *Finder) GetSpotPricing(_ context.Context, _ []aws.InstanceTypeResult, _ aws.SpotOptions) ([]aws.SpotPriceResult, error) {
	if m.SpotErr != nil {
		return nil, m.SpotErr
	}
	return m.SpotPrices, nil
}

func (m *Finder) GetCapacityReservations(_ context.Context, _ []string, _ aws.CapacityReservationOptions) ([]aws.CapacityReservationResult, error) {
	if m.ReservationErr != nil {
		return nil, m.ReservationErr
	}
	return m.Reservations, nil
}

func (m *Finder) GetCapacityBlocks(_ context.Context, _ []string, _ aws.CapacityBlockOptions) ([]aws.CapacityBlockResult, error) {
	if m.BlocksErr != nil {
		return nil, m.BlocksErr
	}
	return m.Blocks, nil
}

func (m *Finder) GetCapacityBlockOfferings(_ context.Context, _ []string, _ aws.CapacityBlockOfferingOptions) ([]aws.CapacityBlockOfferingResult, error) {
	if m.BlockOfferingsErr != nil {
		return nil, m.BlockOfferingsErr
	}
	return m.BlockOfferings, nil
}

func (m *Finder) OnDemandPrice(_ context.Context, instanceType, region string) (float64, error) {
	if m.PriceErr != nil {
		return 0, m.PriceErr
	}
	key := instanceType + "/" + region
	if price, ok := m.OnDemandMap[key]; ok {
		return price, nil
	}
	return 0, fmt.Errorf("no mock price for %s in %s", instanceType, region)
}

func (m *Finder) HourlyRate(ctx context.Context, instanceType, region, model string) (float64, error) {
	switch model {
	case "", "on-demand", "ondemand":
		return m.OnDemandPrice(ctx, instanceType, region)
	case "spot":
		for _, sp := range m.SpotPrices {
			if sp.InstanceType == instanceType && sp.Region == region {
				return sp.SpotPrice, nil
			}
		}
		return 0, fmt.Errorf("no mock spot price for %s in %s", instanceType, region)
	default:
		return 0, fmt.Errorf("unknown model %q", model)
	}
}

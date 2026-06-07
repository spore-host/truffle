package aws

import (
	"context"
	"regexp"
)

// Finder is the read-only query interface for EC2 instance discovery.
// The concrete [Client] satisfies this interface. Downstream consumers should
// accept Finder in their function signatures to enable unit testing with
// [awsmock.Finder] or any custom implementation.
type Finder interface {
	// GetEnabledRegions returns regions enabled for the caller's account.
	GetEnabledRegions(ctx context.Context) ([]string, error)

	// GetAllRegions returns all AWS regions (enabled or not).
	GetAllRegions(ctx context.Context) ([]string, error)

	// GetInstanceTypes returns all instance type names available in a region.
	GetInstanceTypes(ctx context.Context, region string) ([]string, error)

	// SearchInstanceTypes finds instance types matching a pattern across regions.
	SearchInstanceTypes(ctx context.Context, regions []string, matcher *regexp.Regexp, opts FilterOptions) ([]InstanceTypeResult, error)

	// GetSpotPricing retrieves current Spot pricing for instance types.
	GetSpotPricing(ctx context.Context, instances []InstanceTypeResult, opts SpotOptions) ([]SpotPriceResult, error)

	// GetCapacityReservations retrieves ODCRs across regions.
	GetCapacityReservations(ctx context.Context, regions []string, opts CapacityReservationOptions) ([]CapacityReservationResult, error)

	// GetCapacityBlocks retrieves Capacity Blocks for ML across regions.
	GetCapacityBlocks(ctx context.Context, regions []string, opts CapacityBlockOptions) ([]CapacityBlockResult, error)

	// OnDemandPrice returns the on-demand $/hr for an instance type in a region.
	OnDemandPrice(ctx context.Context, instanceType, region string) (float64, error)

	// HourlyRate returns $/hr under a given purchase model ("on-demand" or "spot").
	HourlyRate(ctx context.Context, instanceType, region, model string) (float64, error)
}

// compile-time check that *Client implements Finder
var _ Finder = (*Client)(nil)

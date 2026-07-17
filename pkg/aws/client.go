// Package aws provides EC2 capacity discovery for the truffle tool.
// It searches instance type availability, retrieves Spot pricing history,
// and queries On-Demand Capacity Reservations (ODCRs) and Capacity Blocks
// for ML workloads across one or more AWS regions concurrently.
//
// Typical usage:
//
//	client, err := aws.NewClient(ctx)
//	results, err := client.SearchInstanceTypes(ctx, []string{"us-east-1"}, pattern, filterOpts)
//	prices, err := client.GetSpotPricing(ctx, results, aws.SpotOptions{ShowSavings: true})
package aws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
	"github.com/spore-host/truffle/pkg/awscfg"
	"github.com/spore-host/truffle/pkg/spawn"
)

// Client wraps AWS SDK clients
type Client struct {
	cfg aws.Config

	pricerOnce sync.Once
	pricer     OnDemandPricer // on-demand price source; lazily initialized, override with SetOnDemandPricer

	smListerOnce sync.Once
	smLister     SageMakerTypeLister // offered ml.* type source; lazily initialized, override with SetSageMakerTypeLister

	smPricerOnce sync.Once
	smPricer     SageMakerPricer // ml.* on-demand price source; lazily initialized, override with SetSageMakerPricer
}

// InstanceTypeResult represents an instance type's availability and specifications
// in a given region, as returned by [Client.SearchInstanceTypes].
type InstanceTypeResult struct {
	InstanceType    string   `json:"instance_type" yaml:"instance_type"`                                     // EC2 instance type, e.g. "m6i.2xlarge"
	Region          string   `json:"region" yaml:"region"`                                                   // AWS region where this type is available
	AvailableAZs    []string `json:"availability_zones,omitempty" yaml:"availability_zones,omitempty"`       // AZs with capacity; populated when FilterOptions.IncludeAZs is true
	VCPUs           int32    `json:"vcpus,omitempty" yaml:"vcpus,omitempty"`                                 // Default vCPU count
	PhysicalCores   int32    `json:"physical_cores,omitempty" yaml:"physical_cores,omitempty"`               // Physical CPU cores (vCPUs / threads-per-core)
	ThreadsPerCore  int32    `json:"threads_per_core,omitempty" yaml:"threads_per_core,omitempty"`           // Threads per physical core (1 for Graviton, 2 for most x86)
	MemoryMiB       int64    `json:"memory_mib,omitempty" yaml:"memory_mib,omitempty"`                       // Memory in MiB
	Architecture    string   `json:"architecture,omitempty" yaml:"architecture,omitempty"`                   // CPU architecture: "x86_64" or "arm64"
	InstanceFamily  string   `json:"instance_family,omitempty" yaml:"instance_family,omitempty"`             // Family prefix, e.g. "m6i"
	GPUs            int32    `json:"gpus,omitempty" yaml:"gpus,omitempty"`                                   // Number of GPUs; 0 for non-GPU instances
	GPUMemoryMiB    int64    `json:"gpu_memory_mib,omitempty" yaml:"gpu_memory_mib,omitempty"`               // Total GPU memory in MiB across all GPUs
	GPUModel        string   `json:"gpu_model,omitempty" yaml:"gpu_model,omitempty"`                         // GPU model name, e.g. "A100"
	GPUManufacturer string   `json:"gpu_manufacturer,omitempty" yaml:"gpu_manufacturer,omitempty"`           // GPU vendor, e.g. "nvidia"
	OnDemandPrice   float64  `json:"on_demand_price,omitempty" yaml:"on_demand_price,omitempty"`             // On-demand $/hr; 0 if not yet fetched
	SpawnSupported  bool     `json:"spawn_supported,omitempty" yaml:"spawn_supported,omitempty"`             // True if spawn can launch instances in this region
	NestedVirt      bool     `json:"nested_virtualization,omitempty" yaml:"nested_virtualization,omitempty"` // True if the type supports nested virtualization (KVM/Hyper-V in-instance)
	Service         string   `json:"service,omitempty" yaml:"service,omitempty"`                             // Offering namespace: "" / "ec2" (default) or "sagemaker" for ml.* types

	// SageMaker-only fields (populated when Service == "sagemaker"):
	ManagedSpotEligible bool     `json:"managed_spot_eligible,omitempty" yaml:"managed_spot_eligible,omitempty"` // Type can be used with managed spot training (has a "spot training job usage" quota). Managed spot is a billed-time discount (up to 90%), not a spot market — there is no per-type spot price.
	TrainingJobQuota    *float64 `json:"training_job_quota,omitempty" yaml:"training_job_quota,omitempty"`       // Account limit for "training job usage" of this type; nil when the region exposes no such quota. 0 means an increase must be requested before launching.
}

// SpotPriceResult represents a Spot instance price observation for one AZ,
// as returned by [Client.GetSpotPricing].
type SpotPriceResult struct {
	InstanceType     string  `json:"instance_type" yaml:"instance_type"`                         // EC2 instance type
	Region           string  `json:"region" yaml:"region"`                                       // AWS region
	AvailabilityZone string  `json:"availability_zone" yaml:"availability_zone"`                 // AZ where this price applies
	SpotPrice        float64 `json:"spot_price" yaml:"spot_price"`                               // Current Spot price in $/hr
	OnDemandPrice    float64 `json:"on_demand_price,omitempty" yaml:"on_demand_price,omitempty"` // On-demand $/hr for savings calculation; 0 if unavailable
	SavingsPercent   float64 `json:"savings_percent,omitempty" yaml:"savings_percent,omitempty"` // Discount vs on-demand: 100*(1-spot/ondemand); set when SpotOptions.ShowSavings is true
	Timestamp        string  `json:"timestamp" yaml:"timestamp"`                                 // RFC3339 timestamp of the price observation
	ProductType      string  `json:"product_type,omitempty" yaml:"product_type,omitempty"`       // OS description, e.g. "Linux/UNIX"
}

// FilterOptions controls which instance types are returned by [Client.SearchInstanceTypes].
type FilterOptions struct {
	IncludeAZs       bool    // If true, populate InstanceTypeResult.AvailableAZs (one extra API call per type)
	Architecture     string  // Filter to "x86_64" or "arm64"; empty matches both
	MinVCPUs         int     // Minimum vCPU count; 0 disables this filter
	MinMemory        float64 // Minimum memory in GiB; 0 disables this filter
	MinPhysicalCores int     // Minimum physical core count; 0 disables this filter
	ExactVCPUs       bool    // If true, match exact vCPU count instead of minimum
	ExactMemory      bool    // If true, match exact memory instead of minimum
	ExactCores       bool    // If true, match exact physical core count instead of minimum
	InstanceFamily   string  // Restrict to a family prefix, e.g. "m6i"; empty matches all
	NestedVirt       bool    // If true, only types supporting nested virtualization (KVM/Hyper-V in-instance)
	Verbose          bool    // If true, log per-region progress to stderr
}

// SpotOptions controls the behavior of [Client.GetSpotPricing].
type SpotOptions struct {
	MaxPrice      float64 // Exclude results above this $/hr threshold; 0 disables
	ShowSavings   bool    // Populate SpotPriceResult.SavingsPercent by comparing with on-demand price
	LookbackHours int     // Hours of price history to query; 0 uses the AWS default (typically 1 hour)
	OnlyActive    bool    // Exclude price history entries with no current offering
	Verbose       bool    // If true, log per-region progress to stderr
}

// CapacityReservationResult represents an ODCR (On-Demand Capacity Reservation)
type CapacityReservationResult struct {
	ReservationID     string   `json:"reservation_id" yaml:"reservation_id"`
	InstanceType      string   `json:"instance_type" yaml:"instance_type"`
	Region            string   `json:"region" yaml:"region"`
	AvailabilityZone  string   `json:"availability_zone" yaml:"availability_zone"`
	TotalCapacity     int32    `json:"total_capacity" yaml:"total_capacity"`
	AvailableCapacity int32    `json:"available_capacity" yaml:"available_capacity"`
	UsedCapacity      int32    `json:"used_capacity" yaml:"used_capacity"`
	State             string   `json:"state" yaml:"state"`
	Tenancy           string   `json:"tenancy" yaml:"tenancy"`
	EBSOptimized      bool     `json:"ebs_optimized" yaml:"ebs_optimized"`
	EndDate           string   `json:"end_date,omitempty" yaml:"end_date,omitempty"`
	Platform          string   `json:"platform" yaml:"platform"`
	Tags              []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// CapacityReservationOptions contains ODCR search options
type CapacityReservationOptions struct {
	InstanceTypes  []string
	OnlyAvailable  bool  // Only show reservations with available capacity
	OnlyActive     bool  // Only show active reservations
	IncludeExpired bool  // Include expired reservations
	MinCapacity    int32 // Minimum available capacity
	Verbose        bool
}

// CapacityBlockResult represents a Capacity Block for ML
type CapacityBlockResult struct {
	CapacityBlockID       string   `json:"capacity_block_id" yaml:"capacity_block_id"`
	InstanceType          string   `json:"instance_type" yaml:"instance_type"`
	InstanceCount         int32    `json:"instance_count" yaml:"instance_count"`
	AvailabilityZone      string   `json:"availability_zone" yaml:"availability_zone"`
	StartDate             string   `json:"start_date" yaml:"start_date"`
	EndDate               string   `json:"end_date" yaml:"end_date"`
	DurationHours         int32    `json:"duration_hours" yaml:"duration_hours"`
	State                 string   `json:"state" yaml:"state"` // scheduled, active, completed, cancelled
	ReservationFee        float64  `json:"reservation_fee" yaml:"reservation_fee"`
	UltraClusterPlacement bool     `json:"ultra_cluster_placement" yaml:"ultra_cluster_placement"`
	Tags                  []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// CapacityBlockOptions contains Capacity Block search options
type CapacityBlockOptions struct {
	InstanceTypes []string
	MinDuration   int32  // Minimum duration in hours
	MaxDuration   int32  // Maximum duration in hours
	StartAfter    string // ISO format datetime
	StartBefore   string // ISO format datetime
	OnlyActive    bool
	Verbose       bool
}

// CapacityBlockOfferingResult represents a PURCHASABLE Capacity Block offering
// (the answer to "what can I reserve?"), as returned by DescribeCapacityBlock
// Offerings — distinct from CapacityBlockResult, which represents an EXISTING
// reservation you already own. The OfferingID is what spawn purchases (spawn#217).
type CapacityBlockOfferingResult struct {
	OfferingID       string `json:"offering_id" yaml:"offering_id"`
	InstanceType     string `json:"instance_type" yaml:"instance_type"`
	InstanceCount    int32  `json:"instance_count" yaml:"instance_count"`
	AvailabilityZone string `json:"availability_zone" yaml:"availability_zone"`
	Region           string `json:"region" yaml:"region"`
	StartDate        string `json:"start_date" yaml:"start_date"`
	EndDate          string `json:"end_date" yaml:"end_date"`
	DurationHours    int32  `json:"duration_hours" yaml:"duration_hours"`
	UpfrontFee       string `json:"upfront_fee" yaml:"upfront_fee"` // total up-front price (string per AWS)
	CurrencyCode     string `json:"currency_code" yaml:"currency_code"`
	Tenancy          string `json:"tenancy,omitempty" yaml:"tenancy,omitempty"`
}

// CapacityBlockOfferingOptions are the query parameters for discovering
// purchasable Capacity Block offerings. CapacityDurationHours and InstanceType
// are required by the AWS DescribeCapacityBlockOfferings API; InstanceCount
// defaults to 1.
type CapacityBlockOfferingOptions struct {
	InstanceType          string // required
	InstanceCount         int32  // required by us; defaults to 1
	CapacityDurationHours int32  // required by AWS
	StartAfter            string // ISO-8601; earliest block start → StartDateRange
	EndBy                 string // ISO-8601; latest block end → EndDateRange
	Verbose               bool
}

// NewClient creates a new AWS client using the shared spore.host profile/region
// (flag > env > file), falling back to the ambient credential chain when none is
// set. Region is otherwise applied per-request.
func NewClient(ctx context.Context) (*Client, error) {
	cfg, err := awscfg.Load(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %w", err)
	}
	return NewClientFromConfig(cfg), nil
}

// NewClientFromConfig creates a new AWS client with an injected aws.Config.
// Use this in tests to point the client at a Substrate emulator.
func NewClientFromConfig(cfg aws.Config) *Client {
	return &Client{cfg: cfg}
}

// GetEnabledRegions returns AWS regions enabled for this account.
// This respects Service Control Policies (SCPs) that may restrict regions.
// Regions blocked by organizational SCPs will not appear in the returned list.
func (c *Client) GetEnabledRegions(ctx context.Context) ([]string, error) {
	client := ec2.NewFromConfig(c.cfg)

	// DescribeRegions with AllRegions=false returns only enabled regions
	// This automatically respects SCPs and account-level region restrictions
	result, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: boolPtr(false), // Only enabled regions
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe regions: %w", err)
	}

	regions := make([]string, 0, len(result.Regions))
	for _, region := range result.Regions {
		if region.RegionName != nil {
			regions = append(regions, *region.RegionName)
		}
	}

	return regions, nil
}

// GetAllRegions is deprecated. Use GetEnabledRegions instead.
// This method is kept for backward compatibility.
func (c *Client) GetAllRegions(ctx context.Context) ([]string, error) {
	return c.GetEnabledRegions(ctx)
}

// GetInstanceTypes returns all instance types in a region
func (c *Client) GetInstanceTypes(ctx context.Context, region string) ([]string, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	var instanceTypes []string
	paginator := ec2.NewDescribeInstanceTypesPaginator(client, &ec2.DescribeInstanceTypesInput{})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to describe instance types in %s: %w", region, err)
		}

		for _, it := range output.InstanceTypes {
			instanceTypes = append(instanceTypes, string(it.InstanceType))
		}
	}

	return instanceTypes, nil
}

// SearchInstanceTypes searches for instance types matching the pattern across regions
func (c *Client) SearchInstanceTypes(ctx context.Context, regions []string, matcher *regexp.Regexp, opts FilterOptions) ([]InstanceTypeResult, error) {
	var (
		results []InstanceTypeResult
		mu      sync.Mutex
		wg      sync.WaitGroup
		errCh   = make(chan error, len(regions))
	)

	// Limit concurrent region queries
	semaphore := make(chan struct{}, 10)

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  Checking region: %s\n", r)
			}

			regionResults, err := c.searchInRegion(ctx, r, matcher, opts)
			if err != nil {
				errCh <- fmt.Errorf("region %s: %w", r, err)
				return
			}

			if len(regionResults) > 0 {
				mu.Lock()
				results = append(results, regionResults...)
				mu.Unlock()
			}
		}(region)
	}

	wg.Wait()
	close(errCh)

	// Collect any per-region errors. A total failure (expired creds, throttling,
	// an SCP denying the API) must not masquerade as a legitimate empty result —
	// truffle is the discovery authority spawn/lagotto consume, so a masked
	// failure makes callers conclude a type/region is unavailable when the query
	// never actually ran (#63).
	var regionErrs []error
	for err := range errCh {
		regionErrs = append(regionErrs, err)
	}

	if len(regions) > 0 && len(regionErrs) == len(regions) {
		// Every region failed — surface it rather than returning empty success.
		return results, fmt.Errorf("all %d region queries failed: %w", len(regions), errors.Join(regionErrs...))
	}

	// Partial failure: some regions succeeded. Always warn (not just in Verbose)
	// so a silently-degraded result is visible.
	if len(regionErrs) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: %d of %d region queries failed; results may be incomplete:\n", len(regionErrs), len(regions))
		for _, err := range regionErrs {
			fmt.Fprintf(os.Stderr, "    %v\n", err)
		}
	}

	return results, nil
}

func (c *Client) searchInRegion(ctx context.Context, region string, matcher *regexp.Regexp, opts FilterOptions) ([]InstanceTypeResult, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	var results []InstanceTypeResult

	// Optimize: if pattern matches specific instance type(s), use API filter
	input := &ec2.DescribeInstanceTypesInput{}
	if specificTypes := extractSpecificTypes(matcher); len(specificTypes) > 0 {
		input.InstanceTypes = specificTypes
	}

	paginator := ec2.NewDescribeInstanceTypesPaginator(client, input)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			// When the caller searched for specific instance type(s) via the API
			// filter, a type that simply isn't offered in this region comes back
			// as InvalidInstanceType / InvalidParameterValue. That's a legitimate
			// "no match here", not a region failure — returning it as an error
			// would (post-#63) turn an unavailable-type search into a hard failure
			// when every searched region lacks the type.
			if len(input.InstanceTypes) > 0 && isInstanceTypeNotOffered(err) {
				return nil, nil
			}
			return nil, err
		}

		for _, it := range output.InstanceTypes {
			instanceType := string(it.InstanceType)

			// Check if matches pattern
			if !matcher.MatchString(instanceType) {
				continue
			}

			// Apply filters
			if !matchesFilters(it, opts) {
				continue
			}

			result := buildResultFromEC2(it, instanceType, region)
			result.SpawnSupported = spawn.IsSpawnSupported(region)

			// Get availability zones if requested
			if opts.IncludeAZs {
				azs, err := c.getAvailabilityZones(ctx, region, instanceType)
				if err == nil {
					result.AvailableAZs = azs
				}
			}

			results = append(results, result)
		}
	}

	return results, nil
}

// buildResultFromEC2 maps an EC2 DescribeInstanceTypes record into an
// InstanceTypeResult, populating vCPU/memory/architecture/GPU specs. The
// displayType is the name used for InstanceType and family extraction — for
// EC2 it's the raw type (e.g. "g5.2xlarge"); the SageMaker path passes the
// "ml."-prefixed name (e.g. "ml.g5.2xlarge") so the row reads in the ml.*
// namespace while still deriving specs from the underlying EC2 type.
// It does not set SpawnSupported, AvailableAZs, OnDemandPrice, or Service —
// callers own those.
func buildResultFromEC2(it types.InstanceTypeInfo, displayType, region string) InstanceTypeResult {
	cores := valueOrZero(it.VCpuInfo.DefaultCores)
	threadsPerCore := valueOrZero(it.VCpuInfo.DefaultThreadsPerCore)
	if threadsPerCore == 0 {
		threadsPerCore = 1
	}

	result := InstanceTypeResult{
		InstanceType:   displayType,
		Region:         region,
		VCPUs:          valueOrZero(it.VCpuInfo.DefaultVCpus),
		PhysicalCores:  cores,
		ThreadsPerCore: threadsPerCore,
		MemoryMiB:      valueOrZero(it.MemoryInfo.SizeInMiB),
		InstanceFamily: extractFamily(displayType),
	}

	// Get architecture + nested-virtualization support
	if len(it.ProcessorInfo.SupportedArchitectures) > 0 {
		result.Architecture = string(it.ProcessorInfo.SupportedArchitectures[0])
	}
	result.NestedVirt = supportsNestedVirt(it)

	// Get GPU info
	if it.GpuInfo != nil && len(it.GpuInfo.Gpus) > 0 {
		for _, gpu := range it.GpuInfo.Gpus {
			result.GPUs += valueOrZero(gpu.Count)
			if gpu.Name != nil {
				result.GPUModel = *gpu.Name
			}
			if gpu.Manufacturer != nil {
				result.GPUManufacturer = *gpu.Manufacturer
			}
			if gpu.MemoryInfo != nil && gpu.MemoryInfo.SizeInMiB != nil {
				result.GPUMemoryMiB = int64(*gpu.MemoryInfo.SizeInMiB) * int64(valueOrZero(gpu.Count))
			}
		}
		// Use total if per-GPU not available
		if result.GPUMemoryMiB == 0 && it.GpuInfo.TotalGpuMemoryInMiB != nil {
			result.GPUMemoryMiB = int64(valueOrZero(it.GpuInfo.TotalGpuMemoryInMiB))
		}
	}

	return result
}

func (c *Client) getAvailabilityZones(ctx context.Context, region, instanceType string) ([]string, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	output, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: types.LocationTypeAvailabilityZone,
		Filters: []types.Filter{
			{
				Name:   stringPtr("instance-type"),
				Values: []string{instanceType},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	azs := make([]string, 0, len(output.InstanceTypeOfferings))
	for _, offering := range output.InstanceTypeOfferings {
		if offering.Location != nil {
			azs = append(azs, *offering.Location)
		}
	}

	return azs, nil
}

func matchesFilters(it types.InstanceTypeInfo, opts FilterOptions) bool {
	// Architecture filter
	if opts.Architecture != "" {
		archMatch := false
		for _, arch := range it.ProcessorInfo.SupportedArchitectures {
			if string(arch) == opts.Architecture {
				archMatch = true
				break
			}
		}
		if !archMatch {
			return false
		}
	}

	// vCPU filter
	if opts.MinVCPUs > 0 {
		vcpus := valueOrZero(it.VCpuInfo.DefaultVCpus)
		if opts.ExactVCPUs {
			// Exact match
			if int(vcpus) != opts.MinVCPUs {
				return false
			}
		} else {
			// Minimum match
			if int(vcpus) < opts.MinVCPUs {
				return false
			}
		}
	}

	// Physical core filter
	if opts.MinPhysicalCores > 0 {
		cores := valueOrZero(it.VCpuInfo.DefaultCores)
		if cores == 0 {
			// Estimate: most x86 = 2 threads/core, Graviton = 1
			tpc := valueOrZero(it.VCpuInfo.DefaultThreadsPerCore)
			if tpc == 0 {
				tpc = 2
			}
			cores = valueOrZero(it.VCpuInfo.DefaultVCpus) / tpc
		}
		if opts.ExactCores {
			if int(cores) != opts.MinPhysicalCores {
				return false
			}
		} else {
			if int(cores) < opts.MinPhysicalCores {
				return false
			}
		}
	}

	// Memory filter (convert GiB to MiB)
	if opts.MinMemory > 0 {
		memMiB := valueOrZero(it.MemoryInfo.SizeInMiB)
		memGiB := float64(memMiB) / 1024.0
		if opts.ExactMemory {
			// Exact match (with 0.01 GiB tolerance for floating point)
			if memGiB < opts.MinMemory-0.01 || memGiB > opts.MinMemory+0.01 {
				return false
			}
		} else {
			// Minimum match
			if memGiB < opts.MinMemory {
				return false
			}
		}
	}

	// Instance family filter
	if opts.InstanceFamily != "" {
		family := extractFamily(string(it.InstanceType))
		if family != opts.InstanceFamily {
			return false
		}
	}

	// Nested-virtualization filter
	if opts.NestedVirt && !supportsNestedVirt(it) {
		return false
	}

	return true
}

// supportsNestedVirt reports whether an instance type can run a hypervisor
// (KVM/Hyper-V) inside the instance. AWS advertises this in
// ProcessorInfo.SupportedFeatures (e.g. virtual C8i/M8i/R8i as of Feb 2026);
// no hardcoded family list — the API is the source of truth.
func supportsNestedVirt(it types.InstanceTypeInfo) bool {
	if it.ProcessorInfo == nil {
		return false
	}
	for _, f := range it.ProcessorInfo.SupportedFeatures {
		if f == types.SupportedAdditionalProcessorFeatureNestedVirtualization {
			return true
		}
	}
	return false
}

// Capabilities is a feature-support snapshot for a single instance type, from
// one DescribeInstanceTypes call. truffle is the instance-type capability
// authority for the spore.host tools; other tools (e.g. spawn's pre-flight
// launch validation) consume this instead of re-querying EC2 themselves.
type Capabilities struct {
	InstanceType         string   `json:"instance_type"`
	Found                bool     `json:"found"`
	Architectures        []string `json:"architectures,omitempty"` // e.g. ["x86_64"], ["arm64"]
	ClusterPlacement     bool     `json:"cluster_placement"`       // supports the "cluster" placement strategy (MPI)
	EFA                  bool     `json:"efa"`                     // supports Elastic Fabric Adapter
	Hibernation          bool     `json:"hibernation"`             // supports On-Demand hibernation
	NestedVirtualization bool     `json:"nested_virtualization"`   // can run KVM/Hyper-V in-instance
	GPUs                 int32    `json:"gpus,omitempty"`
	BareMetal            bool     `json:"bare_metal"`
}

// GetCapabilities returns feature support for a single instance type in the
// given region (region may be empty for the client default). Backed by one
// DescribeInstanceTypes call; the API is the source of truth (no hardcoded
// family lists).
func (c *Client) GetCapabilities(ctx context.Context, instanceType, region string) (*Capabilities, error) {
	cfg := c.cfg
	if region != "" {
		cfg.Region = region
	}
	ec2Client := ec2.NewFromConfig(cfg)
	out, err := ec2Client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err != nil {
		return nil, fmt.Errorf("describe instance type %s: %w", instanceType, err)
	}
	caps := &Capabilities{InstanceType: instanceType}
	if len(out.InstanceTypes) == 0 {
		return caps, nil // Found=false
	}
	it := out.InstanceTypes[0]
	caps.Found = true
	caps.NestedVirtualization = supportsNestedVirt(it)
	caps.Hibernation = it.HibernationSupported != nil && *it.HibernationSupported
	caps.BareMetal = it.BareMetal != nil && *it.BareMetal
	if it.NetworkInfo != nil && it.NetworkInfo.EfaSupported != nil {
		caps.EFA = *it.NetworkInfo.EfaSupported
	}
	if it.PlacementGroupInfo != nil {
		for _, s := range it.PlacementGroupInfo.SupportedStrategies {
			if s == types.PlacementGroupStrategyCluster {
				caps.ClusterPlacement = true
			}
		}
	}
	if it.GpuInfo != nil {
		for _, g := range it.GpuInfo.Gpus {
			if g.Count != nil {
				caps.GPUs += *g.Count
			}
		}
	}
	if it.ProcessorInfo != nil {
		for _, a := range it.ProcessorInfo.SupportedArchitectures {
			caps.Architectures = append(caps.Architectures, string(a))
		}
	}
	return caps, nil
}

func extractFamily(instanceType string) string {
	// Extract family from instance type (e.g., "m5" from "m5.large")
	for i, c := range instanceType {
		if c == '.' {
			return instanceType[:i]
		}
	}
	return instanceType
}

// isInstanceTypeNotOffered reports whether an error from DescribeInstanceTypes
// (called with an explicit InstanceTypes filter) means the requested type simply
// isn't offered in that region — AWS returns InvalidInstanceType or
// InvalidParameterValue for that case. Such an error is a clean "no match here",
// not a failed query.
func isInstanceTypeNotOffered(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "InvalidInstanceType", "InvalidParameterValue":
			return true
		}
	}
	return false
}

// extractSpecificTypes analyzes regex and returns specific instance types if pattern is exact
func extractSpecificTypes(matcher *regexp.Regexp) []types.InstanceType {
	pattern := matcher.String()

	// Check if pattern is a specific instance type (no regex metacharacters except ^$)
	// Simple heuristic: if it contains *, +, ?, [, ], (, ), |, then it's a wildcard
	if containsWildcard(pattern) {
		return nil // Wildcard pattern, need to fetch all
	}

	// Remove ^ and $ anchors if present
	pattern = strings.TrimPrefix(pattern, "^")
	pattern = strings.TrimSuffix(pattern, "$")

	// Unescape regex escapes like \. -> .
	pattern = strings.ReplaceAll(pattern, "\\.", ".")

	// Pattern is specific, return as filter
	return []types.InstanceType{types.InstanceType(pattern)}
}

func containsWildcard(pattern string) bool {
	wildcards := []string{".*", ".+", ".?", "[", "]", "(", ")", "|", "\\d", "\\w", "\\s"}
	for _, wc := range wildcards {
		if strings.Contains(pattern, wc) {
			return true
		}
	}
	return false
}

// GetCapacityBlockOfferings discovers PURCHASABLE Capacity Block offerings across
// regions via DescribeCapacityBlockOfferings (the "what can I reserve?" query) —
// distinct from GetCapacityBlocks, which lists blocks you already own. Read-only.
// The returned OfferingID feeds spawn's purchase command (spawn#217). Price
// (UpfrontFee + CurrencyCode) comes directly from each offering — no separate
// pricing lookup needed.
func (c *Client) GetCapacityBlockOfferings(ctx context.Context, regions []string, opts CapacityBlockOfferingOptions) ([]CapacityBlockOfferingResult, error) {
	var (
		results []CapacityBlockOfferingResult
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	semaphore := make(chan struct{}, 10)

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  Checking Capacity Block offerings in: %s\n", r)
			}

			regionResults, err := c.getRegionCapacityBlockOfferings(ctx, r, opts)
			if err != nil {
				if opts.Verbose {
					fmt.Fprintf(os.Stderr, "  Warning: failed to get Capacity Block offerings for %s: %v\n", r, err)
				}
				return
			}

			if len(regionResults) > 0 {
				mu.Lock()
				results = append(results, regionResults...)
				mu.Unlock()
			}
		}(region)
	}

	wg.Wait()
	return results, nil
}

func (c *Client) getRegionCapacityBlockOfferings(ctx context.Context, region string, opts CapacityBlockOfferingOptions) ([]CapacityBlockOfferingResult, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	count := opts.InstanceCount
	if count <= 0 {
		count = 1
	}

	input := &ec2.DescribeCapacityBlockOfferingsInput{
		CapacityDurationHours: aws.Int32(opts.CapacityDurationHours),
		InstanceType:          stringPtr(opts.InstanceType),
		InstanceCount:         aws.Int32(count),
	}
	if opts.StartAfter != "" {
		if t, err := time.Parse(time.RFC3339, opts.StartAfter); err == nil {
			input.StartDateRange = &t
		}
	}
	if opts.EndBy != "" {
		if t, err := time.Parse(time.RFC3339, opts.EndBy); err == nil {
			input.EndDateRange = &t
		}
	}

	var results []CapacityBlockOfferingResult

	paginator := ec2.NewDescribeCapacityBlockOfferingsPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range output.CapacityBlockOfferings {
			startDate := ""
			if o.StartDate != nil {
				startDate = o.StartDate.Format(time.RFC3339)
			}
			endDate := ""
			if o.EndDate != nil {
				endDate = o.EndDate.Format(time.RFC3339)
			}
			results = append(results, CapacityBlockOfferingResult{
				OfferingID:       valueOrZero(o.CapacityBlockOfferingId),
				InstanceType:     valueOrZero(o.InstanceType),
				InstanceCount:    valueOrZero(o.InstanceCount),
				AvailabilityZone: valueOrZero(o.AvailabilityZone),
				Region:           region,
				StartDate:        startDate,
				EndDate:          endDate,
				DurationHours:    valueOrZero(o.CapacityBlockDurationHours),
				UpfrontFee:       valueOrZero(o.UpfrontFee),
				CurrencyCode:     valueOrZero(o.CurrencyCode),
				Tenancy:          string(o.Tenancy),
			})
		}
	}

	return results, nil
}

func valueOrZero[T any](ptr *T) T {
	if ptr != nil {
		return *ptr
	}
	var zero T
	return zero
}

func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

// GetSpotPricing retrieves current Spot pricing for instance types
func (c *Client) GetSpotPricing(ctx context.Context, instances []InstanceTypeResult, opts SpotOptions) ([]SpotPriceResult, error) {
	var (
		results []SpotPriceResult
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	// Group instances by region
	regionInstances := make(map[string][]InstanceTypeResult)
	for _, inst := range instances {
		regionInstances[inst.Region] = append(regionInstances[inst.Region], inst)
	}

	errCh := make(chan error, len(regionInstances))

	// Limit concurrent region queries
	semaphore := make(chan struct{}, 10)

	for region, regInstances := range regionInstances {
		wg.Add(1)
		go func(r string, insts []InstanceTypeResult) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  Fetching Spot prices in: %s\n", r)
			}

			regionResults, err := c.getRegionSpotPricing(ctx, r, insts, opts)
			if err != nil {
				errCh <- fmt.Errorf("region %s: %w", r, err)
				return
			}

			if len(regionResults) > 0 {
				mu.Lock()
				results = append(results, regionResults...)
				mu.Unlock()
			}
		}(region, regInstances)
	}

	wg.Wait()
	close(errCh)

	// As with SearchInstanceTypes: a total failure must not look like "no spot
	// data available" (#63).
	var regionErrs []error
	for err := range errCh {
		regionErrs = append(regionErrs, err)
	}

	nRegions := len(regionInstances)
	if nRegions > 0 && len(regionErrs) == nRegions {
		return results, fmt.Errorf("all %d region Spot price queries failed: %w", nRegions, errors.Join(regionErrs...))
	}
	if len(regionErrs) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: %d of %d region Spot price queries failed; results may be incomplete:\n", len(regionErrs), nRegions)
		for _, err := range regionErrs {
			fmt.Fprintf(os.Stderr, "    %v\n", err)
		}
	}

	return results, nil
}

func (c *Client) getRegionSpotPricing(ctx context.Context, region string, instances []InstanceTypeResult, opts SpotOptions) ([]SpotPriceResult, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	var results []SpotPriceResult

	// Calculate start time for price history
	startTime := time.Now().Add(-time.Duration(opts.LookbackHours) * time.Hour)

	// Deduplicate instances by type (callers may pass the same type multiple times)
	seen := make(map[string]bool)
	var deduped []InstanceTypeResult
	for _, inst := range instances {
		if !seen[inst.InstanceType] {
			seen[inst.InstanceType] = true
			deduped = append(deduped, inst)
		}
	}

	// Query each instance type. Track query failures so that a region where
	// *every* query failed (e.g. expired creds, throttling) reports an error to
	// the caller instead of an empty slice — otherwise a total failure looks
	// like "no spot data available" (#63).
	var lastErr error
	failed := 0
	for _, inst := range deduped {
		// When the caller asks for savings, fetch the on-demand rate once per
		// instance type (it does not vary by AZ) and reuse it across this type's AZs.
		var onDemand float64
		if opts.ShowSavings {
			onDemand, _ = c.OnDemandPrice(ctx, inst.InstanceType, region)
		}

		// Get Spot price history
		input := &ec2.DescribeSpotPriceHistoryInput{
			InstanceTypes: []types.InstanceType{types.InstanceType(inst.InstanceType)},
			StartTime:     &startTime,
			ProductDescriptions: []string{
				"Linux/UNIX",
			},
		}

		output, err := client.DescribeSpotPriceHistory(ctx, input)
		if err != nil {
			lastErr = err
			failed++
			continue // Skip this type; a per-type failure is tolerated unless all fail.
		}

		// When lookback is > 1 hour, return all price history points (for trend analysis).
		// Otherwise, deduplicate to latest price per AZ (default: current spot price).
		if opts.LookbackHours > 1 {
			for i := range output.SpotPriceHistory {
				sp := &output.SpotPriceHistory[i]
				if sp.AvailabilityZone == nil || sp.SpotPrice == nil {
					continue
				}
				price := parsePrice(*sp.SpotPrice)
				if opts.MaxPrice > 0 && price > opts.MaxPrice {
					continue
				}
				result := SpotPriceResult{
					InstanceType:     inst.InstanceType,
					Region:           region,
					AvailabilityZone: *sp.AvailabilityZone,
					SpotPrice:        price,
					ProductType:      string(sp.ProductDescription),
				}
				if opts.ShowSavings && onDemand > 0 {
					result.OnDemandPrice = onDemand
					if price > 0 {
						result.SavingsPercent = (1 - price/onDemand) * 100
					}
				}
				if sp.Timestamp != nil {
					result.Timestamp = sp.Timestamp.Format(time.RFC3339)
				}
				results = append(results, result)
			}
		} else {
			// Default: group by AZ and get latest price
			azPrices := make(map[string]*types.SpotPrice)
			for i := range output.SpotPriceHistory {
				sp := &output.SpotPriceHistory[i]
				if sp.AvailabilityZone == nil || sp.SpotPrice == nil {
					continue
				}
				az := *sp.AvailabilityZone
				if existing, ok := azPrices[az]; !ok || (sp.Timestamp != nil && existing.Timestamp != nil && sp.Timestamp.After(*existing.Timestamp)) {
					azPrices[az] = sp
				}
			}

			for az, spotPrice := range azPrices {
				if spotPrice.SpotPrice == nil {
					continue
				}
				price := parsePrice(*spotPrice.SpotPrice)
				if opts.MaxPrice > 0 && price > opts.MaxPrice {
					continue
				}
				result := SpotPriceResult{
					InstanceType:     inst.InstanceType,
					Region:           region,
					AvailabilityZone: az,
					SpotPrice:        price,
					ProductType:      string(spotPrice.ProductDescription),
				}
				if opts.ShowSavings && onDemand > 0 {
					result.OnDemandPrice = onDemand
					if price > 0 {
						result.SavingsPercent = (1 - price/onDemand) * 100
					}
				}
				if spotPrice.Timestamp != nil {
					result.Timestamp = spotPrice.Timestamp.Format(time.RFC3339)
				}
				results = append(results, result)
			}
		}
	}

	// If we attempted at least one query and every one failed, the region query
	// failed — don't return an empty-but-successful result (#63).
	if len(deduped) > 0 && failed == len(deduped) {
		return nil, fmt.Errorf("all %d Spot price queries in %s failed: %w", failed, region, lastErr)
	}

	return results, nil
}

func parsePrice(priceStr string) float64 {
	// AWS returns price as string, convert to float
	var price float64
	_, _ = fmt.Sscanf(priceStr, "%f", &price)
	return price
}

// GetCapacityReservations retrieves On-Demand Capacity Reservations (ODCRs)
func (c *Client) GetCapacityReservations(ctx context.Context, regions []string, opts CapacityReservationOptions) ([]CapacityReservationResult, error) {
	var (
		results []CapacityReservationResult
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	// Limit concurrent region queries
	semaphore := make(chan struct{}, 10)

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  Checking capacity reservations in: %s\n", r)
			}

			regionResults, err := c.getRegionCapacityReservations(ctx, r, opts)
			if err != nil {
				if opts.Verbose {
					fmt.Fprintf(os.Stderr, "  Warning: failed to get capacity reservations for %s: %v\n", r, err)
				}
				return
			}

			if len(regionResults) > 0 {
				mu.Lock()
				results = append(results, regionResults...)
				mu.Unlock()
			}
		}(region)
	}

	wg.Wait()
	return results, nil
}

func (c *Client) getRegionCapacityReservations(ctx context.Context, region string, opts CapacityReservationOptions) ([]CapacityReservationResult, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	// Build filters
	filters := []types.Filter{}

	if len(opts.InstanceTypes) > 0 {
		filters = append(filters, types.Filter{
			Name:   stringPtr("instance-type"),
			Values: opts.InstanceTypes,
		})
	}

	if opts.OnlyActive {
		filters = append(filters, types.Filter{
			Name:   stringPtr("state"),
			Values: []string{"active"},
		})
	}

	input := &ec2.DescribeCapacityReservationsInput{
		Filters: filters,
	}

	var results []CapacityReservationResult

	// Paginate through results
	paginator := ec2.NewDescribeCapacityReservationsPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, cr := range output.CapacityReservations {
			// Apply filters
			if opts.OnlyAvailable && valueOrZero(cr.AvailableInstanceCount) == 0 {
				continue
			}

			if opts.MinCapacity > 0 && valueOrZero(cr.AvailableInstanceCount) < opts.MinCapacity {
				continue
			}

			state := string(cr.State)
			if !opts.IncludeExpired && (state == "expired" || state == "cancelled") {
				continue
			}

			result := CapacityReservationResult{
				ReservationID:     valueOrZero(cr.CapacityReservationId),
				InstanceType:      valueOrZero(cr.InstanceType),
				Region:            region,
				AvailabilityZone:  valueOrZero(cr.AvailabilityZone),
				TotalCapacity:     valueOrZero(cr.TotalInstanceCount),
				AvailableCapacity: valueOrZero(cr.AvailableInstanceCount),
				State:             state,
				Tenancy:           string(cr.Tenancy),
				EBSOptimized:      valueOrZero(cr.EbsOptimized),
				Platform:          string(cr.InstancePlatform),
			}

			result.UsedCapacity = result.TotalCapacity - result.AvailableCapacity

			if cr.EndDate != nil {
				result.EndDate = cr.EndDate.Format(time.RFC3339)
			}

			// Extract tags
			for _, tag := range cr.Tags {
				if tag.Key != nil && tag.Value != nil {
					result.Tags = append(result.Tags, fmt.Sprintf("%s=%s", *tag.Key, *tag.Value))
				}
			}

			results = append(results, result)
		}
	}

	return results, nil
}

// GetCapacityBlocks retrieves Capacity Blocks for ML
func (c *Client) GetCapacityBlocks(ctx context.Context, regions []string, opts CapacityBlockOptions) ([]CapacityBlockResult, error) {
	var (
		results []CapacityBlockResult
		mu      sync.Mutex
		wg      sync.WaitGroup
	)

	// Limit concurrent region queries
	semaphore := make(chan struct{}, 10)

	for _, region := range regions {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "  Checking Capacity Blocks for ML in: %s\n", r)
			}

			regionResults, err := c.getRegionCapacityBlocks(ctx, r, opts)
			if err != nil {
				if opts.Verbose {
					fmt.Fprintf(os.Stderr, "  Warning: failed to get Capacity Blocks for %s: %v\n", r, err)
				}
				return
			}

			if len(regionResults) > 0 {
				mu.Lock()
				results = append(results, regionResults...)
				mu.Unlock()
			}
		}(region)
	}

	wg.Wait()
	return results, nil
}

func (c *Client) getRegionCapacityBlocks(ctx context.Context, region string, opts CapacityBlockOptions) ([]CapacityBlockResult, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	// Build filters for DescribeCapacityBlockOfferings
	// Note: This API is for FINDING available blocks to reserve
	// For EXISTING reservations, we use DescribeCapacityReservations with specific filters

	// Get existing Capacity Block reservations
	// Capacity Blocks are a special type of Capacity Reservation
	filters := []types.Filter{
		{
			Name:   stringPtr("instance-type"),
			Values: opts.InstanceTypes,
		},
		{
			// Capacity Blocks have a specific reservation type
			Name:   stringPtr("capacity-reservation-type"),
			Values: []string{"capacity-block"},
		},
	}

	if opts.OnlyActive {
		filters = append(filters, types.Filter{
			Name:   stringPtr("state"),
			Values: []string{"active", "scheduled"},
		})
	}

	input := &ec2.DescribeCapacityReservationsInput{
		Filters: filters,
	}

	var results []CapacityBlockResult

	paginator := ec2.NewDescribeCapacityReservationsPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, cr := range output.CapacityReservations {
			// Capacity Blocks have specific characteristics
			// They have start/end dates and are co-located in UltraClusters

			startDate := ""
			if cr.StartDate != nil {
				startDate = cr.StartDate.Format(time.RFC3339)
			}

			endDate := ""
			durationHours := int32(0)
			if cr.EndDate != nil {
				endDate = cr.EndDate.Format(time.RFC3339)
				if cr.StartDate != nil {
					durationHours = int32(cr.EndDate.Sub(*cr.StartDate).Hours())
				}
			}

			// Filter by duration if specified
			if opts.MinDuration > 0 && durationHours < opts.MinDuration {
				continue
			}
			if opts.MaxDuration > 0 && durationHours > opts.MaxDuration {
				continue
			}

			result := CapacityBlockResult{
				CapacityBlockID:       valueOrZero(cr.CapacityReservationId),
				InstanceType:          valueOrZero(cr.InstanceType),
				InstanceCount:         valueOrZero(cr.TotalInstanceCount),
				AvailabilityZone:      valueOrZero(cr.AvailabilityZone),
				StartDate:             startDate,
				EndDate:               endDate,
				DurationHours:         durationHours,
				State:                 string(cr.State),
				UltraClusterPlacement: true, // Capacity Blocks are always in UltraClusters
			}

			// Extract tags
			for _, tag := range cr.Tags {
				if tag.Key != nil && tag.Value != nil {
					result.Tags = append(result.Tags, fmt.Sprintf("%s=%s", *tag.Key, *tag.Value))
				}
			}

			results = append(results, result)
		}
	}

	return results, nil
}

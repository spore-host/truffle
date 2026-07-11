package aws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/quotas"
)

// SageMakerTypeLister returns the set of SageMaker ml.* instance types offered
// in a region. There is no SageMaker equivalent of EC2 DescribeInstanceTypes,
// so the authoritative source is Service Quotas (which lists a quota per ml.*
// type). Implementations should be safe for concurrent use.
//
// The default implementation queries Service Quotas via pkg/quotas; embedders
// and tests can inject their own with [Client.SetSageMakerTypeLister].
type SageMakerTypeLister interface {
	// OfferedTypes returns the ml.*-prefixed instance types offered in region.
	OfferedTypes(ctx context.Context, region string) ([]string, error)
}

// quotaSageMakerLister is the default SageMakerTypeLister, backed by the AWS
// Service Quotas API through pkg/quotas.
type quotaSageMakerLister struct {
	c *quotas.ServiceQuotasClient
}

func (q quotaSageMakerLister) OfferedTypes(ctx context.Context, region string) ([]string, error) {
	return quotas.OfferedSageMakerTypes(ctx, q.c, region)
}

// SetSageMakerTypeLister overrides the source of offered ml.* types used by
// this client. Pass nil to reset to the default Service Quotas lister. This is
// primarily for embedders and tests that want a deterministic offering set.
func (c *Client) SetSageMakerTypeLister(l SageMakerTypeLister) {
	c.smListerOnce.Do(func() {}) // mark initialized so the default is not installed later
	c.smLister = l
}

// sageMakerTypeLister returns the active lister, lazily installing the default
// Service Quotas-backed lister on first use.
func (c *Client) sageMakerTypeLister() SageMakerTypeLister {
	c.smListerOnce.Do(func() {
		if c.smLister == nil {
			c.smLister = quotaSageMakerLister{c: quotas.NewServiceQuotasClientFromConfig(c.cfg)}
		}
	})
	return c.smLister
}

// SearchSageMakerInstanceTypes searches for SageMaker ml.* instance types
// matching the pattern across regions. It mirrors [Client.SearchInstanceTypes]:
// per-region concurrency and the #63 error-aggregation contract (a total
// failure must not masquerade as an empty result).
//
// Unlike EC2, SageMaker has no DescribeInstanceTypes API, so the offered set
// comes from Service Quotas. Specs (vCPU/memory/GPU/arch) are derived from the
// underlying EC2 instance type (ml.g5.2xlarge → g5.2xlarge), which runs on the
// same hardware. Results carry Service="sagemaker" and SpawnSupported=false
// (spawn launches EC2, not SageMaker); OnDemandPrice is left 0 — SageMaker
// pricing is a separate concern (issue #80).
func (c *Client) SearchSageMakerInstanceTypes(ctx context.Context, regions []string, matcher *regexp.Regexp, opts FilterOptions) ([]InstanceTypeResult, error) {
	var (
		results []InstanceTypeResult
		mu      sync.Mutex
		wg      sync.WaitGroup
		errCh   = make(chan error, len(regions))
	)

	// Limit concurrent region queries (matches SearchInstanceTypes).
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

			regionResults, err := c.searchSageMakerInRegion(ctx, r, matcher, opts)
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
	// truffle is the discovery authority spawn/lagotto consume (#63).
	var regionErrs []error
	for err := range errCh {
		regionErrs = append(regionErrs, err)
	}

	if len(regions) > 0 && len(regionErrs) == len(regions) {
		return results, fmt.Errorf("all %d region queries failed: %w", len(regions), errors.Join(regionErrs...))
	}

	if len(regionErrs) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: %d of %d region queries failed; results may be incomplete:\n", len(regionErrs), len(regions))
		for _, err := range regionErrs {
			fmt.Fprintf(os.Stderr, "    %v\n", err)
		}
	}

	return results, nil
}

func (c *Client) searchSageMakerInRegion(ctx context.Context, region string, matcher *regexp.Regexp, opts FilterOptions) ([]InstanceTypeResult, error) {
	// 1. Offered set: which ml.* types exist in this region (Service Quotas).
	offered, err := c.sageMakerTypeLister().OfferedTypes(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("list offered SageMaker types: %w", err)
	}

	// 2. Pattern filter. Match against the full ml.-prefixed name (so an explicit
	//    "ml.g5.*" pattern works) OR the base EC2 name (so natural-language
	//    queries, whose matcher is built for EC2-style names like "^g5\.", also
	//    match). Track base EC2 type → ml.* name for spec enrichment.
	baseToML := make(map[string]string)
	var baseTypes []types.InstanceType
	for _, mlType := range offered {
		base := strings.TrimPrefix(mlType, "ml.")
		if !matcher.MatchString(mlType) && !matcher.MatchString(base) {
			continue
		}
		if _, dup := baseToML[base]; dup {
			continue
		}
		baseToML[base] = mlType
		baseTypes = append(baseTypes, types.InstanceType(base))
	}

	if len(baseTypes) == 0 {
		return nil, nil
	}

	// 3. Spec enrichment: one DescribeInstanceTypes call for the base EC2 types.
	cfg := c.cfg
	cfg.Region = region
	ec2Client := ec2.NewFromConfig(cfg)

	enriched := make(map[string]types.InstanceTypeInfo)
	paginator := ec2.NewDescribeInstanceTypesPaginator(ec2Client, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: baseTypes,
	})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			// A base type not offered as EC2 in this region comes back as
			// InvalidInstanceType/InvalidParameterValue — that's not a region
			// failure (the ml.* type may still be validly offered via quota).
			// Fall through to emit quota-only rows with zeroed specs.
			if isInstanceTypeNotOffered(err) {
				break
			}
			return nil, err
		}
		for _, it := range output.InstanceTypes {
			enriched[string(it.InstanceType)] = it
		}
	}

	// 4. Build results. Every offered+matched ml.* type produces a row; specs
	//    come from the EC2 peer when available, else zeroed (availability is
	//    real even when the EC2 spec lookup didn't return the type).
	var results []InstanceTypeResult
	for base, mlType := range baseToML {
		if it, ok := enriched[base]; ok {
			// Apply spec-based filters against the EC2 record.
			if !matchesFilters(it, opts) {
				continue
			}
			result := buildResultFromEC2(it, mlType, region)
			// Family should reflect the ml.* namespace (e.g. "ml.g5"), not "ml".
			result.InstanceFamily = "ml." + extractFamily(base)
			result.Service = "sagemaker"
			result.SpawnSupported = false
			results = append(results, result)
			continue
		}

		// No EC2 peer returned — emit a quota-only row (specs unknown). Skip it
		// if spec-based filters were requested, since we can't verify them.
		if opts.Architecture != "" || opts.MinVCPUs > 0 || opts.MinMemory > 0 ||
			opts.MinPhysicalCores > 0 || opts.NestedVirt {
			continue
		}
		if opts.InstanceFamily != "" && "ml."+extractFamily(base) != opts.InstanceFamily {
			continue
		}
		results = append(results, InstanceTypeResult{
			InstanceType:   mlType,
			Region:         region,
			InstanceFamily: "ml." + extractFamily(base),
			Service:        "sagemaker",
			SpawnSupported: false,
		})
	}

	return results, nil
}

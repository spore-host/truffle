package find

import (
	"regexp"
	"strings"

	"github.com/spore-host/libs/catalog"
	"github.com/spore-host/truffle/pkg/aws"
)

// SearchCriteria holds the compiled, ready-to-execute form of a [ParsedQuery].
// Pass InstanceTypePattern and FilterOptions directly to aws.SearchInstanceTypes.
type SearchCriteria struct {
	InstanceTypePattern *regexp.Regexp    // Compiled regexp matching eligible EC2 instance type strings
	FilterOptions       aws.FilterOptions // Numeric and categorical filters passed to SearchInstanceTypes
}

// BuildCriteria converts a ParsedQuery into SearchCriteria for execution.
// includeAZs controls whether the search itself performs the (expensive: one
// extra API round-trip per matched instance type, done serially — truffle#141)
// per-type availability-zone lookup. This is a CLI-flag concern (--skip-azs),
// not something derived from the query text itself, so it's a parameter here
// rather than a ParsedQuery field.
func (pq *ParsedQuery) BuildCriteria(includeAZs bool) (*SearchCriteria, error) {
	sc := &SearchCriteria{
		FilterOptions: aws.FilterOptions{
			IncludeAZs:       includeAZs,
			MinVCPUs:         pq.MinVCPU,
			MinMemory:        pq.MinMemory,
			MinPhysicalCores: pq.MinPhysCores,
			ExactVCPUs:       pq.ExactMatch,
			ExactMemory:      pq.ExactMatch,
			ExactCores:       pq.ExactMatch,
			Architecture:     pq.DeriveArchitecture(),
			NestedVirt:       pq.RequireNestedV,
		},
	}

	// Apply hardware minimums from app catalog entries.
	// Only applied when the user has not specified explicit constraints.
	for _, appName := range pq.Apps {
		if entry, ok := catalog.Lookup(appName); ok {
			if sc.FilterOptions.MinVCPUs == 0 && entry.MinVCPUs > 0 {
				sc.FilterOptions.MinVCPUs = entry.MinVCPUs
			}
			if sc.FilterOptions.MinMemory == 0 && entry.MinMemoryGiB > 0 {
				sc.FilterOptions.MinMemory = float64(entry.MinMemoryGiB)
			}
		}
	}

	// Build instance type pattern
	pattern := pq.buildInstanceTypePattern()
	sc.InstanceTypePattern = regexp.MustCompile(pattern)

	return sc, nil
}

func (pq *ParsedQuery) buildInstanceTypePattern() string {
	// If GPUs is the only active dimension, use the exact-instance-type
	// shortcut (tighter than the family-level pattern below). Once any other
	// dimension (EFA, MIG, vendor, processor, instruction set, network speed)
	// is also present, that shortcut must NOT be taken under AND — jumping
	// straight to the GPU's exact instance types would silently discard the
	// other constraints entirely (#144). Under OR, the shortcut is skipped
	// too, since GPU should union at the family level with everything else,
	// consistent with how every other dimension already combines.
	if len(pq.GPUs) > 0 && pq.Operator == OperatorAnd && !pq.hasOtherActiveDimensions() {
		instances := pq.ResolveGPUInstances()
		if len(instances) > 0 {
			// Exact match on instance types, keeping only the requested sizes
			// when the query names any (#211).
			var escaped []string
			sized := regexp.MustCompile(pq.sizeOnlyPattern())
			for _, inst := range instances {
				if sized.MatchString(inst) {
					escaped = append(escaped, regexp.QuoteMeta(inst))
				}
			}
			if len(escaped) == 0 {
				// No instance with this GPU comes in the requested size.
				return `^$`
			}
			return "^(" + strings.Join(escaped, "|") + ")$"
		}
	}

	// Otherwise, resolve instance families
	families := pq.ResolveInstanceFamilies()

	if len(families) == 0 {
		if pq.HasNoMatchingFamily() {
			// The query's family constraints (or its app's families and the
			// query's) have no family in common, e.g. "intel h100". Matching
			// every family instead searched the whole catalog (#229).
			return `^$`
		}
		// No family constraint: any family, but still only the requested
		// sizes. Returning ".*" here dropped the size words of queries such as
		// "arm64 large" (#211).
		return pq.sizeOnlyPattern()
	}

	// Build pattern with family and optional size constraints
	escapedFamilies := make([]string, len(families))
	for i, family := range families {
		escapedFamilies[i] = regexp.QuoteMeta(family)
	}

	familyPattern := "(" + strings.Join(escapedFamilies, "|") + ")"

	// Add size constraints if present
	if pq.hasSizes() {
		sizePattern := pq.BuildSizePattern()
		return "^" + familyPattern + "\\." + sizePattern + "$"
	}

	// Match any size for the families
	return "^" + familyPattern + "\\..*$"
}

// Matcher returns a function suitable for aws.SearchInstanceTypes
func (sc *SearchCriteria) Matcher() func(string) bool {
	return func(instanceType string) bool {
		return sc.InstanceTypePattern.MatchString(instanceType)
	}
}

// sizeOnlyPattern matches any instance type whose size is one the query asks
// for (e.g. "large" -> 2xlarge or 4xlarge), or every type when it names none.
func (pq *ParsedQuery) sizeOnlyPattern() string {
	if !pq.hasSizes() {
		return ".*"
	}
	return `^[^.]+\.` + pq.BuildSizePattern() + "$"
}

// hasSizes reports whether the query names a size word or an instance size.
func (pq *ParsedQuery) hasSizes() bool {
	return len(pq.Sizes) > 0 || len(pq.InstanceSizes) > 0
}

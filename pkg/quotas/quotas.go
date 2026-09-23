// Package quotas queries AWS Service Quotas and current EC2 usage to determine
// whether a given instance type can be launched under an account's limits.
//
// EC2 service quotas are expressed as per-family vCPU counts rather than
// instance counts. This package handles the mapping from instance type to
// quota family (e.g., p4d.24xlarge → FamilyP) and computes remaining capacity.
//
// Typical usage:
//
//	client, err := quotas.NewClient(ctx)
//	info, err := client.GetQuotas(ctx, "us-east-1")
//	ok, msg := client.CanLaunch("p4d.24xlarge", 96, info, false)
//
// Results are cached per region for 5 minutes to avoid redundant API calls.
package quotas

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"github.com/spore-host/truffle/pkg/awscfg"
)

// QuotaFamily represents instance family groupings for Service Quotas
type QuotaFamily string

const (
	FamilyStandard QuotaFamily = "Standard" // A, C, D, H, I, M, R, T, Z
	FamilyF        QuotaFamily = "F"        // FPGA
	FamilyG        QuotaFamily = "G"        // Graphics (g4dn, g5, g6) — quota shared with VT
	FamilyP        QuotaFamily = "P"        // GPU Training (p3, p4, p5)
	FamilyX        QuotaFamily = "X"        // Memory optimized
	FamilyInf      QuotaFamily = "Inf"      // Inferentia
	FamilyTrn      QuotaFamily = "Trn"      // Trainium
	FamilyDL       QuotaFamily = "DL"       // Deep Learning (dl1 Habana Gaudi, dl2q Qualcomm)
)

// Service Quota codes for EC2
const (
	// On-Demand vCPU quotas
	QuotaCodeStandard = "L-1216C47A" // Running On-Demand Standard instances
	QuotaCodeF        = "L-74FC7D96" // Running On-Demand F instances
	QuotaCodeG        = "L-DB2E81BA" // Running On-Demand G instances
	QuotaCodeP        = "L-417A185B" // Running On-Demand P instances
	QuotaCodeX        = "L-7295265B" // Running On-Demand X instances
	QuotaCodeInf      = "L-1945791B" // Running On-Demand Inf instances
	QuotaCodeTrn      = "L-2C3B7624" // Running On-Demand Trn instances
	QuotaCodeDL       = "L-6E869C2A" // Running On-Demand DL instances (dl1/dl2q)

	// Spot vCPU quotas
	QuotaCodeSpotStandard = "L-34B43A08" // All Standard Spot Instance Requests
	QuotaCodeSpotF        = "L-88CF9481" // All F Spot Instance Requests
	QuotaCodeSpotG        = "L-3819A6DF" // All G and VT Spot Instance Requests
	QuotaCodeSpotP        = "L-7212CCBC" // All P Spot Instance Requests
	QuotaCodeSpotX        = "L-E3A00192" // All X Spot Instance Requests
	QuotaCodeSpotInf      = "L-B5D1601B" // All Inf Spot Instance Requests
	QuotaCodeSpotTrn      = "L-5480EFD2" // All Trn Spot Instance Requests
	QuotaCodeSpotDL       = "L-85EED4F7" // All DL Spot Instance Requests
)

// onDemandQuotaCodes and spotQuotaCodes are the SINGLE SOURCE OF TRUTH for the
// (family, lifecycle) → Service Quotas code mapping. [Client.GetQuotas] fetches
// from them and [QuotaIncreaseCommand] emits from them, so "the quota we read"
// and "the quota we tell you to raise" can no longer drift.
//
// They exist because they did drift (#167): QuotaIncreaseCommand carried its own
// partial switch that defaulted to the Standard On-Demand code L-1216C47A, so an
// X/DL/F on-demand user — or ANY non-G/P spot user — was handed a copy-pasteable
// command that raised an unrelated limit (and, on the spot path, the wrong
// lifecycle entirely), waited for approval, and was still blocked. Any new
// QuotaFamily must be added here, and an unmapped family is reported as unmapped
// rather than silently answered with another family's code.
var (
	onDemandQuotaCodes = map[QuotaFamily]string{
		FamilyStandard: QuotaCodeStandard,
		FamilyF:        QuotaCodeF,
		FamilyG:        QuotaCodeG,
		FamilyP:        QuotaCodeP,
		FamilyX:        QuotaCodeX,
		FamilyInf:      QuotaCodeInf,
		FamilyTrn:      QuotaCodeTrn,
		FamilyDL:       QuotaCodeDL,
	}

	spotQuotaCodes = map[QuotaFamily]string{
		FamilyStandard: QuotaCodeSpotStandard,
		FamilyF:        QuotaCodeSpotF,
		FamilyG:        QuotaCodeSpotG,
		FamilyP:        QuotaCodeSpotP,
		FamilyX:        QuotaCodeSpotX,
		FamilyInf:      QuotaCodeSpotInf,
		FamilyTrn:      QuotaCodeSpotTrn,
		FamilyDL:       QuotaCodeSpotDL,
	}
)

// QuotaCodeFor returns the AWS Service Quotas code for a family and lifecycle
// (spot=true for the Spot request quota, false for the On-Demand running-instance
// quota).
//
// ok is false when this package has no code for the pair. Callers MUST NOT
// substitute another family's code in that case — handing the user the Standard
// On-Demand code for an X-family or Trn-spot shortfall is exactly the bug #167
// fixed. Fall back to the Service Quotas console instead, the way
// [QuotaIncreaseCommand] does.
func QuotaCodeFor(family QuotaFamily, spot bool) (string, bool) {
	if spot {
		code, ok := spotQuotaCodes[family]
		return code, ok
	}
	code, ok := onDemandQuotaCodes[family]
	return code, ok
}

// lifecycleLabel renders a quota's lifecycle the way AWS names it.
func lifecycleLabel(spot bool) string {
	if spot {
		return "Spot"
	}
	return "On-Demand"
}

// QuotaInfo holds quota limits and current usage for a single region,
// as returned by [Client.GetQuotas].
type QuotaInfo struct {
	Region string // AWS region this snapshot covers, e.g. "us-east-1"

	// On-Demand quotas — maximum vCPUs per family
	OnDemand map[QuotaFamily]int32

	// Spot quotas — maximum vCPUs per family for Spot instances
	Spot map[QuotaFamily]int32

	// Current On-Demand usage — vCPUs currently in use (running + pending) per
	// family, EXCLUDING Spot instances. Only ever subtracted from OnDemand.
	Usage map[QuotaFamily]int32

	// Current Spot usage — vCPUs currently in use (running + pending) per
	// family, counting ONLY Spot instances (distinguished via EC2's
	// InstanceLifecycle field, #132). Subtracted from Spot in CanLaunch, the
	// same way Usage is subtracted from OnDemand — before this field existed,
	// CanLaunch's Spot path could confirm a request fit the FULL Spot quota but
	// had no way to confirm remaining headroom, so an account already at its
	// Spot ceiling got a false "fits" with no signal the quota was saturated.
	SpotUsage map[QuotaFamily]int32

	// OnDemandErrors and SpotErrors record per-family quota-READ failures, keyed
	// by the family whose lookup failed (#167). A family present here is absent
	// from OnDemand/Spot: its limit is unknown, NOT zero.
	//
	// Without this, the two states were indistinguishable — a one-value map read
	// returns 0 both for "the account genuinely has zero vCPUs of this family"
	// and for "we could not ask", where "could not ask" covers a missing
	// servicequotas:GetServiceQuota permission, throttling, and a region that
	// does not expose the code. Callers that want to say "your quota is 0" should
	// confirm the key is present (two-value read) and that no error is recorded
	// here; [QuotaInfo.LookupError] and [QuotaInfo.MissingFamilies] wrap that.
	//
	// Both maps are non-nil after a successful [Client.GetQuotas] and empty when
	// every lookup succeeded. They may be nil on a hand-built QuotaInfo.
	OnDemandErrors map[QuotaFamily]error
	SpotErrors     map[QuotaFamily]error

	RunningInstances     int32     // Current count of running+pending instances in this region
	RunningInstancesMax  int32     // Per-region instance count limit (typically 20 for new accounts)
	LastUpdated          time.Time // When this snapshot was fetched
	CredentialsAvailable bool      // False when quotas were estimated due to missing credentials
}

// LookupError returns the error recorded for a family's quota lookup, or nil if
// the lookup succeeded (or was never attempted). See [QuotaInfo.OnDemandErrors].
func (q *QuotaInfo) LookupError(family QuotaFamily, spot bool) error {
	if q == nil {
		return nil
	}
	if spot {
		return q.SpotErrors[family]
	}
	return q.OnDemandErrors[family]
}

// MissingFamilies returns the families whose quota could not be read for the
// given lifecycle, sorted for stable output. Use it to tell the user "I could not
// determine these quotas" instead of reporting them as zero (#167).
func (q *QuotaInfo) MissingFamilies(spot bool) []QuotaFamily {
	if q == nil {
		return nil
	}
	errs := q.OnDemandErrors
	if spot {
		errs = q.SpotErrors
	}
	if len(errs) == 0 {
		return nil
	}
	out := make([]QuotaFamily, 0, len(errs))
	for family := range errs {
		out = append(out, family)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Client handles quota operations
type Client struct {
	sqClient  *servicequotas.Client
	ec2Client *ec2.Client
	baseCfg   aws.Config
	cache     map[string]*QuotaInfo
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration

	// Test seams. Each is nil in production and falls back to the real method of
	// the same name; a unit test sets them to exercise GetQuotas' bookkeeping
	// (notably the per-family error recording added for #167) without any AWS
	// call. Narrow function fields rather than a wide interface, since only
	// GetQuotas' three reads need substituting.
	quotaValueFn   func(ctx context.Context, region, quotaCode string) (int32, error)
	usageFn        func(ctx context.Context, region string) (onDemand, spot map[QuotaFamily]int32, err error)
	runningCountFn func(ctx context.Context, region string) (int32, error)
}

func (c *Client) quotaValue(ctx context.Context, region, quotaCode string) (int32, error) {
	if c.quotaValueFn != nil {
		return c.quotaValueFn(ctx, region, quotaCode)
	}
	return c.getQuotaValue(ctx, region, quotaCode)
}

func (c *Client) currentUsage(ctx context.Context, region string) (onDemand, spot map[QuotaFamily]int32, err error) {
	if c.usageFn != nil {
		return c.usageFn(ctx, region)
	}
	return c.getCurrentUsage(ctx, region)
}

func (c *Client) runningInstanceCount(ctx context.Context, region string) (int32, error) {
	if c.runningCountFn != nil {
		return c.runningCountFn(ctx, region)
	}
	return c.getRunningInstanceCount(ctx, region)
}

// NewClient creates a quota client using the default credential chain.
// Returns error if AWS credentials are not available.
func NewClient(ctx context.Context) (*Client, error) {
	// Shared profile/region (flag > env > file), falling back to us-east-1 when
	// nothing sets a region — quota checks must target a real region.
	cfg, err := awscfg.Load(ctx, "us-east-1")
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config (credentials required for quota checking): %w", err)
	}
	return NewClientFromConfig(cfg), nil
}

// NewClientFromConfig creates a quota client with an injected AWS config.
// Use this in tests to point the client at a Substrate emulator.
func NewClientFromConfig(cfg aws.Config) *Client {
	return &Client{
		sqClient:  servicequotas.NewFromConfig(cfg),
		ec2Client: ec2.NewFromConfig(cfg),
		baseCfg:   cfg,
		cache:     make(map[string]*QuotaInfo),
		cacheTTL:  5 * time.Minute,
	}
}

// GetQuotas retrieves quota information for a region
func (c *Client) GetQuotas(ctx context.Context, region string) (*QuotaInfo, error) {
	// Check cache
	c.cacheMu.RLock()
	if cached, ok := c.cache[region]; ok {
		if time.Since(cached.LastUpdated) < c.cacheTTL {
			c.cacheMu.RUnlock()
			return cached, nil
		}
	}
	c.cacheMu.RUnlock()

	// Fetch fresh data
	info := &QuotaInfo{
		Region:               region,
		OnDemand:             make(map[QuotaFamily]int32),
		Spot:                 make(map[QuotaFamily]int32),
		Usage:                make(map[QuotaFamily]int32),
		OnDemandErrors:       make(map[QuotaFamily]error),
		SpotErrors:           make(map[QuotaFamily]error),
		LastUpdated:          time.Now(),
		CredentialsAvailable: true,
	}

	// Per-quota failures don't fail the whole snapshot (a region may not offer a
	// code at all), but they are RECORDED and logged rather than dropped — on
	// failure the family key stays absent from OnDemand/Spot, and the recorded
	// error is what lets a caller say "couldn't read" instead of "is zero" (#167).
	c.fetchFamilyQuotas(ctx, region, onDemandQuotaCodes, false, info.OnDemand, info.OnDemandErrors)
	c.fetchFamilyQuotas(ctx, region, spotQuotaCodes, true, info.Spot, info.SpotErrors)

	// Get current usage, split by lifecycle (#132).
	onDemandUsage, spotUsage, err := c.currentUsage(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to get current usage: %w", err)
	}
	info.Usage = onDemandUsage
	info.SpotUsage = spotUsage

	// Get running instance count
	runningCount, err := c.runningInstanceCount(ctx, region)
	if err == nil {
		info.RunningInstances = runningCount
	}

	// Running instances quota is typically 20 by default
	info.RunningInstancesMax = 20 // Could query this too

	// Cache result
	c.cacheMu.Lock()
	c.cache[region] = info
	c.cacheMu.Unlock()

	return info, nil
}

// fetchFamilyQuotas reads one quota per family into values, recording any
// per-family failure in errs (and logging it) instead of discarding it. A family
// whose lookup fails is deliberately left ABSENT from values so that a two-value
// map read still distinguishes "unknown" from a genuine zero limit (#167).
func (c *Client) fetchFamilyQuotas(
	ctx context.Context,
	region string,
	codes map[QuotaFamily]string,
	spot bool,
	values map[QuotaFamily]int32,
	errs map[QuotaFamily]error,
) {
	for family, code := range codes {
		value, err := c.quotaValue(ctx, region, code)
		if err != nil {
			// Don't fail the snapshot: a region may not expose this code, and the
			// other families are still useful. But do record and log — the old
			// bare `continue` claimed to log and didn't, which left a permission
			// error looking exactly like a zero quota.
			errs[family] = err
			log.Printf("quotas: could not read %s %s quota (%s) in %s: %v — limit unknown, not zero",
				lifecycleLabel(spot), family, code, region, err)
			continue
		}
		values[family] = value
	}
}

// getQuotaValue retrieves a specific quota value
func (c *Client) getQuotaValue(ctx context.Context, region, quotaCode string) (int32, error) {
	// Clone base config with region override.
	cfg := c.baseCfg
	cfg.Region = region
	sqClient := servicequotas.NewFromConfig(cfg)

	output, err := sqClient.GetServiceQuota(ctx, &servicequotas.GetServiceQuotaInput{
		ServiceCode: aws.String("ec2"),
		QuotaCode:   aws.String(quotaCode),
	})
	if err != nil {
		return 0, err
	}

	if output.Quota != nil && output.Quota.Value != nil {
		return int32(*output.Quota.Value), nil
	}

	return 0, fmt.Errorf("quota value not found")
}

// getCurrentUsage calculates current vCPU usage by family, split by lifecycle
// (on-demand vs Spot, #132) since the two draw against separate quotas. Before
// this split, every running/pending instance's vCPUs landed in one combined
// map that CanLaunch's on-demand branch subtracted from OnDemand alone — which
// double-counted Spot usage against the on-demand quota AND left the Spot
// quota check with no usage signal at all (this package's own long-standing
// comment on that gap, now closed).
func (c *Client) getCurrentUsage(ctx context.Context, region string) (onDemand, spot map[QuotaFamily]int32, err error) {
	cfg := c.baseCfg
	cfg.Region = region
	ec2Client := ec2.NewFromConfig(cfg)

	// Get running instances
	output, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running", "pending"},
			},
		},
	})
	if err != nil {
		return nil, nil, err
	}

	onDemand = make(map[QuotaFamily]int32)
	spot = make(map[QuotaFamily]int32)

	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceType == "" {
				continue
			}

			instanceType := string(instance.InstanceType)
			family := GetQuotaFamily(instanceType)
			vCPUs := getVCPUCount(instanceType)

			// InstanceLifecycle is "spot" for Spot instances and empty ("") for
			// on-demand — there is no dedicated on-demand constant, EC2 just
			// leaves the field unset.
			if instance.InstanceLifecycle == ec2types.InstanceLifecycleTypeSpot {
				spot[family] += vCPUs
			} else {
				onDemand[family] += vCPUs
			}
		}
	}

	return onDemand, spot, nil
}

// getRunningInstanceCount returns the number of running instances
func (c *Client) getRunningInstanceCount(ctx context.Context, region string) (int32, error) {
	cfg := c.baseCfg
	cfg.Region = region
	ec2Client := ec2.NewFromConfig(cfg)

	output, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running", "pending"},
			},
		},
	})
	if err != nil {
		return 0, err
	}

	count := int32(0)
	for _, reservation := range output.Reservations {
		count += int32(len(reservation.Instances))
	}

	return count, nil
}

// CanLaunch reports whether vCPUs more of instanceType fit under the account's
// remaining quota for that family, plus a human explanation when they don't.
//
// A family whose quota could not be READ (key absent from the snapshot, see
// [QuotaInfo.OnDemandErrors]) is reported as undetermined rather than as zero
// — ok is false either way, but the advice differs: "request a quota increase"
// is wrong and actively misleading when the real problem is a missing
// servicequotas:GetServiceQuota permission or a throttled lookup (#167).
func (c *Client) CanLaunch(instanceType string, vCPUs int32, quotas *QuotaInfo, spot bool) (bool, string) {
	family := GetQuotaFamily(instanceType)
	quotaType := lifecycleLabel(spot)

	var quota, usage int32
	var known bool

	if spot {
		quota, known = quotas.Spot[family]
		// SpotUsage is only populated once a caller has fetched quotas via a
		// path that calls getCurrentUsage (GetQuotas). A QuotaInfo built by
		// hand with no SpotUsage map is nil, and a nil map reads as 0 for every
		// key — the same "usage unknown, treat as 0" fallback the on-demand
		// path has always had implicitly (#132).
		usage = quotas.SpotUsage[family]
	} else {
		quota, known = quotas.OnDemand[family]
		usage = quotas.Usage[family]
	}

	if !known {
		msg := fmt.Sprintf("could not determine the %s vCPU quota for %s instances (limit unknown, not zero — check servicequotas:GetServiceQuota access or the Service Quotas console)",
			quotaType, family)
		if err := quotas.LookupError(family, spot); err != nil {
			msg = fmt.Sprintf("could not determine the %s vCPU quota for %s instances: %v (limit unknown, not zero)",
				quotaType, family, err)
		}
		return false, msg
	}

	if quota == 0 {
		return false, fmt.Sprintf("%s quota for %s instances is 0 (request quota increase)", quotaType, family)
	}

	available := quota - usage
	if vCPUs > available {
		return false, fmt.Sprintf("Need %d vCPUs, only %d available (%s %s: quota=%d, usage=%d)",
			vCPUs, available, quotaType, family, quota, usage)
	}

	return true, ""
}

// GetQuotaFamily maps instance type to quota family
func GetQuotaFamily(instanceType string) QuotaFamily {
	// Match on the LETTER-run prefix (the leading alphabetic characters before
	// the first digit), not strings.HasPrefix — otherwise "trn"/"inf" only work
	// by case ordering and multi-letter families like "dl"/"vt" get misfiled
	// under a single-letter case (#64). e.g. "dl1.24xlarge" → "dl", "vt1.3xlarge"
	// → "vt", "p5.48xlarge" → "p".
	alpha := letterPrefix(instanceType)
	switch alpha {
	case "p":
		return FamilyP // GPU training
	case "g":
		return FamilyG // Graphics/GPU
	case "vt":
		// VT (video transcoding) shares the "G and VT" quota, both On-Demand
		// (L-DB2E81BA) and Spot (L-3819A6DF), so it maps to FamilyG.
		return FamilyG
	case "inf":
		return FamilyInf // Inferentia
	case "trn":
		return FamilyTrn // Trainium
	case "dl":
		return FamilyDL // Deep Learning accelerators (dl1 Gaudi, dl2q Qualcomm)
	case "f":
		return FamilyF // FPGA
	case "x":
		return FamilyX // Memory optimized
	default:
		// Standard covers a, c, d, h, i, m, r, t, z, …
		return FamilyStandard
	}
}

// letterPrefix returns the leading run of ASCII letters of an instance type,
// i.e. the family prefix before the generation digit ("p5.48xlarge" → "p",
// "dl2q.24xlarge" → "dl", "trn1.32xlarge" → "trn"). Note it stops at the first
// digit, so "dl2q" → "dl" (the q is a post-digit qualifier), which is what we
// want for family classification.
func letterPrefix(instanceType string) string {
	for i := 0; i < len(instanceType); i++ {
		c := instanceType[i]
		if c < 'a' || c > 'z' {
			return instanceType[:i]
		}
	}
	return instanceType
}

// sizeVCPUs maps the fixed instance-size suffixes to their vCPU count. Sizes not
// listed here fall to the general NxLarge = N*4 pattern in [VCPUsForType].
var sizeVCPUs = map[string]int32{
	"nano":      1,
	"micro":     1,
	"small":     1,
	"medium":    1,
	"large":     2,
	"xlarge":    4,
	"2xlarge":   8,
	"3xlarge":   12,
	"4xlarge":   16,
	"6xlarge":   24,
	"8xlarge":   32,
	"9xlarge":   36,
	"10xlarge":  40,
	"12xlarge":  48,
	"16xlarge":  64,
	"18xlarge":  72,
	"24xlarge":  96,
	"32xlarge":  128,
	"48xlarge":  192,
	"56xlarge":  224,
	"112xlarge": 448,
}

// VCPUsForType returns the vCPU count implied by a LITERAL instance type's size
// suffix, e.g. "p5.48xlarge" → 192. It is the helper for turning a per-family
// vCPU quota (which is how EC2 expresses limits) into an instance count.
//
// This is a HEURISTIC on the size suffix, not a lookup: the authoritative source
// is EC2 DescribeInstanceTypes (see truffle's pkg/aws capability queries), and a
// type whose size AWS prices differently than its name suggests will be wrong
// here. It covers the fixed sub-large sizes and the general NxLarge = N*4 pattern.
//
// ok is false — with a 0 count that callers must NOT read as "zero vCPUs" —
// whenever the type isn't an exact, countable type: a wildcard or regex
// ("g6e.*"), a family-only pattern ("g6e"), an empty string, or a size suffix
// this heuristic doesn't recognize. Handling ok=false explicitly is what keeps
// the heuristic's limits visible at the call site.
func VCPUsForType(instanceType string) (int32, bool) {
	// Patterns aren't types: a caller holding a watch/search pattern must learn
	// that no exact count exists rather than get a number for "g6e.*".
	if strings.ContainsAny(instanceType, "*?[]^$|") {
		return 0, false
	}

	parts := strings.Split(instanceType, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, false
	}
	size := parts[1]

	if v, ok := sizeVCPUs[size]; ok {
		return v, true
	}

	// General pattern: NxLarge has N*4 vCPUs (e.g. "20xlarge" → 80).
	if rest := strings.TrimSuffix(size, "xlarge"); rest != size && rest != "" {
		if num := parseInt(rest); num > 0 {
			return num * 4, true
		}
	}

	return 0, false
}

// getVCPUCount estimates vCPU count from an instance type's size suffix for
// internal usage summing. It wraps [VCPUsForType] and, for a size it can't map,
// logs and falls back to a conservative 2 vCPU (#64) rather than silently
// contributing nothing to the total — the usage sum needs a number, so it can
// only be understated, never silently dropped. Callers that need to KNOW whether
// the type was countable should use [VCPUsForType] directly.
func getVCPUCount(instanceType string) int32 {
	if v, ok := VCPUsForType(instanceType); ok {
		return v
	}
	if parts := strings.Split(instanceType, "."); len(parts) < 2 {
		log.Printf("quotas: cannot parse size from instance type %q; estimating 2 vCPU (usage may be understated)", instanceType)
		return 2
	}
	log.Printf("quotas: unknown instance size in %q; estimating 2 vCPU (usage may be understated — update sizeVCPUs/VCPUsForType)", instanceType)
	return 2
}

func parseInt(s string) int32 {
	var result int32
	_, _ = fmt.Sscanf(s, "%d", &result)
	return result
}

// QuotaIncreaseCommand returns a copy-pasteable AWS CLI block requesting an
// increase to desiredValue vCPUs for the given family and lifecycle, resolving
// the quota code through [QuotaCodeFor].
//
// For a family this package has no code for, it returns a Service Quotas CONSOLE
// pointer instead of a command. That fallback is the point: the previous version
// defaulted an unmapped family to the Standard On-Demand code (L-1216C47A), so a
// user with an X/DL/F on-demand shortfall — or any non-G/P spot shortfall — was
// told to raise an unrelated limit, and on the spot path the wrong lifecycle
// entirely, while the output looked authoritative. Waiting out an approval for the
// wrong quota leaves you exactly as blocked as before (#167).
//
// Use [QuotaCodeFor] if you need to know programmatically whether a command (as
// opposed to a console pointer) is available for a pair.
func QuotaIncreaseCommand(region string, family QuotaFamily, desiredValue int32, spot bool) string {
	quotaCode, ok := QuotaCodeFor(family, spot)
	if !ok {
		// No invented codes: point at the console rather than name a quota we
		// can't vouch for.
		return fmt.Sprintf(`# No Service Quotas code is known for the %s %s vCPU quota.
# Raise it to at least %d vCPUs in the console (do not reuse another family's quota code):
# https://%s.console.aws.amazon.com/servicequotas/home/services/ec2/quotas`,
			family, lifecycleLabel(spot), desiredValue, region)
	}
	quotaName := fmt.Sprintf("%s %s", family, lifecycleLabel(spot))

	return fmt.Sprintf(`# Request %s quota increase to %d vCPUs
aws service-quotas request-service-quota-increase \
  --service-code ec2 \
  --quota-code %s \
  --desired-value %d \
  --region %s

# Check status:
aws service-quotas list-requested-service-quota-change-history-by-quota \
  --service-code ec2 \
  --quota-code %s \
  --region %s`,
		quotaName, desiredValue, quotaCode, desiredValue, region, quotaCode, region)
}

// ── SageMaker quota support ───────────────────────────────────────────────────

// SageMakerQuota holds a single SageMaker service quota entry.
type SageMakerQuota struct {
	Name  string
	Code  string
	Value float64
}

// ServiceQuotasLister can list SageMaker instance quotas.
type ServiceQuotasLister interface {
	ListSageMakerInstanceQuotas(ctx context.Context, region string) ([]SageMakerQuota, error)
}

// Quota-name job-type suffixes (the part after " for " in a SageMaker instance
// quota name, e.g. "ml.g5.2xlarge for training job usage").
const (
	sageMakerJobTraining  = "training job usage"
	sageMakerJobSpotTrain = "spot training job usage"
)

// SageMakerTypeQuota summarizes the per-type SageMaker service quotas relevant
// to discovery, extracted from the region's quota list without extra API calls.
type SageMakerTypeQuota struct {
	// InstanceType is the ml.*-prefixed type, e.g. "ml.g5.2xlarge".
	InstanceType string
	// TrainingJobLimit is the account limit for "training job usage" (the count
	// of concurrent instances of this type in training jobs). -1 when the region
	// exposes no training-job quota for the type.
	TrainingJobLimit float64
	// ManagedSpotEligible is true when the region exposes a "spot training job
	// usage" quota for the type — i.e. the type can be used with managed spot
	// training. (Managed spot is a billed-time discount, not a spot market, so
	// there is no per-type spot price to report; this only marks eligibility.)
	ManagedSpotEligible bool
}

// OfferedSageMakerTypesDetailed returns per-type quota summaries for the ml.*
// instance types offered in a region, sorted by instance type. Quota names look
// like "ml.g5.2xlarge for training job usage"; the type is the part before
// " for " and the job type is the remainder. There is no SageMaker equivalent
// of EC2 DescribeInstanceTypes, so Service Quotas is the authoritative source
// for which ml.* types exist in a region — and, folded in here, for their
// per-type limits and managed-spot eligibility.
func OfferedSageMakerTypesDetailed(ctx context.Context, lister ServiceQuotasLister, region string) ([]SageMakerTypeQuota, error) {
	quotas, err := lister.ListSageMakerInstanceQuotas(ctx, region)
	if err != nil {
		return nil, err
	}

	byType := make(map[string]*SageMakerTypeQuota)
	get := func(t string) *SageMakerTypeQuota {
		q, ok := byType[t]
		if !ok {
			q = &SageMakerTypeQuota{InstanceType: t, TrainingJobLimit: -1}
			byType[t] = q
		}
		return q
	}

	for _, q := range quotas {
		instanceType, jobType := q.Name, ""
		if idx := strings.Index(instanceType, " for "); idx != -1 {
			jobType = instanceType[idx+len(" for "):]
			instanceType = instanceType[:idx]
		}
		if !strings.HasPrefix(instanceType, "ml.") {
			continue
		}
		entry := get(instanceType)
		switch jobType {
		case sageMakerJobTraining:
			entry.TrainingJobLimit = q.Value
		case sageMakerJobSpotTrain:
			entry.ManagedSpotEligible = true
		}
	}

	out := make([]SageMakerTypeQuota, 0, len(byType))
	for _, q := range byType {
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceType < out[j].InstanceType })
	return out, nil
}

// OfferedSageMakerTypes returns the deduplicated, sorted set of ml.* instance
// types offered in a region. It is a thin wrapper over
// [OfferedSageMakerTypesDetailed] for callers that only need the type names.
func OfferedSageMakerTypes(ctx context.Context, lister ServiceQuotasLister, region string) ([]string, error) {
	detailed, err := OfferedSageMakerTypesDetailed(ctx, lister, region)
	if err != nil {
		return nil, err
	}
	types := make([]string, len(detailed))
	for i, d := range detailed {
		types[i] = d.InstanceType
	}
	return types, nil
}

// ServiceQuotasClient wraps the AWS Service Quotas API for SageMaker queries.
type ServiceQuotasClient struct {
	cfg aws.Config
}

// NewServiceQuotasClient creates a new client for querying service quotas.
func NewServiceQuotasClient(ctx context.Context) (*ServiceQuotasClient, error) {
	cfg, err := awscfg.Load(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &ServiceQuotasClient{cfg: cfg}, nil
}

// NewServiceQuotasClientFromConfig creates a service-quotas client from an
// injected aws.Config. Use this to share a config (e.g. a test's Substrate
// emulator, or an already-loaded config) rather than loading a fresh one.
func NewServiceQuotasClientFromConfig(cfg aws.Config) *ServiceQuotasClient {
	return &ServiceQuotasClient{cfg: cfg}
}

// ListSageMakerInstanceQuotas returns all SageMaker ml.* instance quota entries
// for the given region. Only quotas whose name starts with "ml." are returned.
func (c *ServiceQuotasClient) ListSageMakerInstanceQuotas(ctx context.Context, region string) ([]SageMakerQuota, error) {
	cfg := c.cfg.Copy()
	cfg.Region = region
	sqc := servicequotas.NewFromConfig(cfg)

	var results []SageMakerQuota
	paginator := servicequotas.NewListServiceQuotasPaginator(sqc, &servicequotas.ListServiceQuotasInput{
		ServiceCode: aws.String("sagemaker"),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list SageMaker quotas in %s: %w", region, err)
		}
		for _, q := range page.Quotas {
			if q.QuotaName == nil || q.Value == nil || q.QuotaCode == nil {
				continue
			}
			// Only include ml.* instance quotas
			if !strings.HasPrefix(*q.QuotaName, "ml.") {
				continue
			}
			results = append(results, SageMakerQuota{
				Name:  *q.QuotaName,
				Code:  *q.QuotaCode,
				Value: *q.Value,
			})
		}
	}
	return results, nil
}

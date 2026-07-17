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

// QuotaInfo holds quota limits and current usage for a single region,
// as returned by [Client.GetQuotas].
type QuotaInfo struct {
	Region string // AWS region this snapshot covers, e.g. "us-east-1"

	// On-Demand quotas — maximum vCPUs per family
	OnDemand map[QuotaFamily]int32

	// Spot quotas — maximum vCPUs per family for Spot instances
	Spot map[QuotaFamily]int32

	// Current usage — vCPUs currently in use (running + pending) per family
	Usage map[QuotaFamily]int32

	RunningInstances     int32     // Current count of running+pending instances in this region
	RunningInstancesMax  int32     // Per-region instance count limit (typically 20 for new accounts)
	LastUpdated          time.Time // When this snapshot was fetched
	CredentialsAvailable bool      // False when quotas were estimated due to missing credentials
}

// Client handles quota operations
type Client struct {
	sqClient  *servicequotas.Client
	ec2Client *ec2.Client
	baseCfg   aws.Config
	cache     map[string]*QuotaInfo
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
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
		LastUpdated:          time.Now(),
		CredentialsAvailable: true,
	}

	// Get On-Demand quotas
	quotas := map[QuotaFamily]string{
		FamilyStandard: QuotaCodeStandard,
		FamilyF:        QuotaCodeF,
		FamilyG:        QuotaCodeG,
		FamilyP:        QuotaCodeP,
		FamilyX:        QuotaCodeX,
		FamilyInf:      QuotaCodeInf,
		FamilyTrn:      QuotaCodeTrn,
		FamilyDL:       QuotaCodeDL,
	}

	for family, code := range quotas {
		value, err := c.getQuotaValue(ctx, region, code)
		if err != nil {
			// Log but don't fail - some quotas might not exist
			continue
		}
		info.OnDemand[family] = value
	}

	// Get Spot quotas
	spotQuotas := map[QuotaFamily]string{
		FamilyStandard: QuotaCodeSpotStandard,
		FamilyF:        QuotaCodeSpotF,
		FamilyG:        QuotaCodeSpotG,
		FamilyP:        QuotaCodeSpotP,
		FamilyX:        QuotaCodeSpotX,
		FamilyInf:      QuotaCodeSpotInf,
		FamilyTrn:      QuotaCodeSpotTrn,
		FamilyDL:       QuotaCodeSpotDL,
	}

	for family, code := range spotQuotas {
		value, err := c.getQuotaValue(ctx, region, code)
		if err != nil {
			continue
		}
		info.Spot[family] = value
	}

	// Get current usage
	usage, err := c.getCurrentUsage(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to get current usage: %w", err)
	}
	info.Usage = usage

	// Get running instance count
	runningCount, err := c.getRunningInstanceCount(ctx, region)
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

// getCurrentUsage calculates current vCPU usage by family
func (c *Client) getCurrentUsage(ctx context.Context, region string) (map[QuotaFamily]int32, error) {
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
		return nil, err
	}

	usage := make(map[QuotaFamily]int32)

	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceType == "" {
				continue
			}

			instanceType := string(instance.InstanceType)
			family := GetQuotaFamily(instanceType)

			// Get vCPU count for instance type
			vCPUs := getVCPUCount(instanceType)
			usage[family] += vCPUs
		}
	}

	return usage, nil
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

// CanLaunch checks if an instance can be launched given quotas
func (c *Client) CanLaunch(instanceType string, vCPUs int32, quotas *QuotaInfo, spot bool) (bool, string) {
	family := GetQuotaFamily(instanceType)

	var quota, usage int32
	var quotaType string

	if spot {
		quota = quotas.Spot[family]
		quotaType = "Spot"
		// Current Spot usage is NOT tracked: getCurrentUsage counts running
		// instances without distinguishing lifecycle, and it's only subtracted
		// from the on-demand quota. So for Spot we can confirm the quota exists
		// and that the request fits the *full* quota, but we can't confirm
		// remaining headroom. Don't imply we did.
		if quota == 0 {
			return false, fmt.Sprintf("Spot quota for %s instances is 0 (request quota increase)", family)
		}
		if vCPUs > quota {
			return false, fmt.Sprintf("Need %d vCPUs but the Spot %s quota is only %d", vCPUs, family, quota)
		}
		return true, fmt.Sprintf("fits Spot %s quota (%d vCPUs); current Spot usage is not tracked, so remaining headroom is unverified", family, quota)
	}

	quota = quotas.OnDemand[family]
	usage = quotas.Usage[family]
	quotaType = "On-Demand"

	available := quota - usage

	if quota == 0 {
		return false, fmt.Sprintf("%s quota for %s instances is 0 (request quota increase)", quotaType, family)
	}

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

// getVCPUCount estimates vCPU count from an instance type's size suffix.
// This is a heuristic (the authoritative source is DescribeInstanceTypes); it
// covers the fixed sub-large sizes and the general NxLarge = N*4 pattern. A size
// it can't map is logged and estimated at the current-usage-summing call site
// (#64) rather than silently treated as a fixed small value.
func getVCPUCount(instanceType string) int32 {
	// Parse size suffix (nano, micro, small, medium, large, xlarge, 2xlarge, etc.)
	parts := strings.Split(instanceType, ".")
	if len(parts) < 2 {
		log.Printf("quotas: cannot parse size from instance type %q; estimating 2 vCPU (usage may be understated)", instanceType)
		return 2
	}

	size := parts[1]

	switch size {
	case "nano":
		return 1
	case "micro":
		return 1
	case "small":
		return 1
	case "medium":
		return 1
	case "large":
		return 2
	case "xlarge":
		return 4
	case "2xlarge":
		return 8
	case "3xlarge":
		return 12
	case "4xlarge":
		return 16
	case "6xlarge":
		return 24
	case "8xlarge":
		return 32
	case "9xlarge":
		return 36
	case "10xlarge":
		return 40
	case "12xlarge":
		return 48
	case "16xlarge":
		return 64
	case "18xlarge":
		return 72
	case "24xlarge":
		return 96
	case "32xlarge":
		return 128
	case "48xlarge":
		return 192
	case "56xlarge":
		return 224
	case "112xlarge":
		return 448
	}

	// General pattern: NxLarge has N*4 vCPUs (e.g. "20xlarge" → 80).
	if strings.HasSuffix(size, "xlarge") {
		numStr := strings.TrimSuffix(size, "xlarge")
		if num := parseInt(numStr); num > 0 {
			return num * 4
		}
	}

	// Truly unknown size — log it (a new size AWS added that we don't map) and
	// fall back to a conservative estimate. Surfaced so it doesn't silently skew
	// the usage total the way a quiet default would.
	log.Printf("quotas: unknown instance size %q in %q; estimating 2 vCPU (usage may be understated — update getVCPUCount)", size, instanceType)
	return 2
}

func parseInt(s string) int32 {
	var result int32
	_, _ = fmt.Sscanf(s, "%d", &result)
	return result
}

// QuotaIncreaseCommand generates AWS CLI command to request quota increase
func QuotaIncreaseCommand(region string, family QuotaFamily, desiredValue int32, spot bool) string {
	quotaCode := QuotaCodeStandard
	quotaName := "Standard On-Demand"

	if spot {
		switch family {
		case FamilyStandard:
			quotaCode = QuotaCodeSpotStandard
			quotaName = "Standard Spot"
		case FamilyG:
			quotaCode = QuotaCodeSpotG
			quotaName = "G Spot"
		case FamilyP:
			quotaCode = QuotaCodeSpotP
			quotaName = "P Spot"
		}
	} else {
		switch family {
		case FamilyStandard:
			quotaCode = QuotaCodeStandard
			quotaName = "Standard On-Demand"
		case FamilyG:
			quotaCode = QuotaCodeG
			quotaName = "G On-Demand"
		case FamilyP:
			quotaCode = QuotaCodeP
			quotaName = "P On-Demand"
		case FamilyInf:
			quotaCode = QuotaCodeInf
			quotaName = "Inf On-Demand"
		case FamilyTrn:
			quotaCode = QuotaCodeTrn
			quotaName = "Trn On-Demand"
		}
	}

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

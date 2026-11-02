package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/quotas"
)

// Obtainability answers "can I actually get this instance type right now, without
// a reservation?" — as distinct from "does it exist and what does it cost?",
// which the search/pricing paths already answer (#108).
//
// For scarce accelerator types the price is often not the deciding number: a type
// can be listed, priced, and completely unobtainable. The three signals here are
// the closest AWS gets to a forward-looking answer, and they fail independently —
// a 1-AZ footprint, a 1/10 spot score and a zero quota are three different
// problems with three different remedies — so each is reported separately rather
// than collapsed into one score.
//
// Deliberately NOT used: RunInstances --dry-run. It validates permissions and
// parameters only, checking neither capacity nor quota, so it reports "would have
// succeeded" for types that cannot be launched at all. See the package docs on
// [Client.Obtainability].
type Obtainability struct {
	InstanceType string `json:"instance_type" yaml:"instance_type"`
	Region       string `json:"region" yaml:"region"`

	// SpotPlacements holds the per-AZ spot placement scores, best first. Empty when
	// the account cannot call GetSpotPlacementScores or the type has no spot
	// offering.
	SpotPlacements []SpotPlacement `json:"spot_placements,omitempty" yaml:"spot_placements,omitempty"`

	// OfferedAZs lists the AZs where the type is offered at all — a hard
	// constraint, not a prediction. A 1-AZ footprint is a materially different risk
	// profile from 6 AZs regardless of price or score. Empty both when the type is
	// offered nowhere and when the lookup failed; OfferedAZsState says which.
	OfferedAZs []string `json:"offered_azs,omitempty" yaml:"offered_azs,omitempty"`

	// OfferedAZsState says how the offered-AZ lookup resolved: collected (then an
	// empty OfferedAZs really means "not offered in this region") or unavailable
	// (the lookup failed, so the footprint is unknown — never zero; the reason is
	// in Warnings). Empty only on a value [Client.Obtainability] did not build
	// (#274).
	OfferedAZsState SignalState `json:"offered_azs_state,omitempty" yaml:"offered_azs_state,omitempty"`

	// TotalAZs is how many AZs the region has, giving OfferedAZs a denominator
	// ("1 of 6" reads very differently from "1 of 1").
	TotalAZs int `json:"total_azs,omitempty" yaml:"total_azs,omitempty"`

	// OnDemandQuotaHeadroom is the vCPUs still available under the relevant
	// On-Demand quota (limit − current usage), floored at 0. This is the gate that
	// actually blocks most accounts: the P-family quota is commonly 0 by default on
	// newer accounts and an increase takes days. Nil when the quota could not be
	// read; QuotaHeadroomState says so.
	OnDemandQuotaHeadroom *int `json:"on_demand_quota_headroom,omitempty" yaml:"on_demand_quota_headroom,omitempty"`

	// QuotaHeadroomState says how the quota-headroom signal resolved: collected
	// (OnDemandQuotaHeadroom holds it, possibly 0) or unavailable (the quota
	// could not be read — a denied servicequotas:GetServiceQuota, throttling, a
	// region without the code — so the limit is unknown, never zero; the reason
	// is in Warnings). Empty only on a value [Client.Obtainability] did not build
	// (#271).
	QuotaHeadroomState SignalState `json:"quota_headroom_state,omitempty" yaml:"quota_headroom_state,omitempty"`

	// QuotaFamily / QuotaCode identify the quota consulted, so a user with no
	// headroom can request an increase against the right limit.
	QuotaFamily string `json:"quota_family,omitempty" yaml:"quota_family,omitempty"`
	QuotaCode   string `json:"quota_code,omitempty" yaml:"quota_code,omitempty"`

	// VCPUsPerInstance is the type's vCPU count, so headroom converts to a usable
	// instance count.
	VCPUsPerInstance int `json:"vcpus_per_instance,omitempty" yaml:"vcpus_per_instance,omitempty"`

	// CapacityBlockOfferings counts the purchasable Capacity Block offerings found
	// in the window described by CapacityBlockWindowHours. A type with no On-Demand
	// or spot prospects can still be reservable this way (and Capacity Blocks
	// routinely undercut On-Demand for accelerator types), so a zero here is a
	// meaningful part of "unobtainable" and a positive number is a real remedy.
	// Nil when the query could not be made or the type is not a Capacity Block
	// type at all; CapacityBlockState says which.
	CapacityBlockOfferings *int `json:"capacity_block_offerings,omitempty" yaml:"capacity_block_offerings,omitempty"`

	// CapacityBlockWindowHours is the duration the offering count was queried for,
	// so the number has a stated scope rather than reading as "ever".
	CapacityBlockWindowHours int `json:"capacity_block_window_hours,omitempty" yaml:"capacity_block_window_hours,omitempty"`

	// CapacityBlockState says how the Capacity Block signal resolved: counted,
	// ineligible (AWS says this type is not sold as a Capacity Block here — a
	// permanent answer, not a failure), or unavailable (the query failed; the
	// reason is in Warnings). Empty only on a value [Client.Obtainability] did
	// not build (#272).
	CapacityBlockState CapacityBlockState `json:"capacity_block_state,omitempty" yaml:"capacity_block_state,omitempty"`

	// OnDemandPrice is the On-Demand $/hr for this type in this region, when a
	// caller resolved it. Price is a separate question from obtainability (a type
	// can be cheap and unobtainable — see the type doc), so the [Client.Obtainability]
	// method leaves this nil; the `available` command populates it so the report
	// and its JSON/YAML form carry the rate alongside the obtainability signals
	// (#159). Nil when the price could not be resolved — reported honestly rather
	// than as a fabricated number.
	OnDemandPrice *float64 `json:"on_demand_price,omitempty" yaml:"on_demand_price,omitempty"`

	// OnDemandPriceSource records where OnDemandPrice came from (the live Price
	// List vs truffle's embedded fallback table), so a stale fallback rate isn't
	// mistaken for the current one. Empty when OnDemandPrice is nil.
	OnDemandPriceSource PriceSource `json:"on_demand_price_source,omitempty" yaml:"on_demand_price_source,omitempty"`

	// Warnings records signals that could not be collected (a denied API, missing
	// credentials). A partial answer is reported rather than a hard failure —
	// GetSpotPlacementScores in particular is commonly denied by SCP — but the gap
	// is never silent, because an absent signal must not read as a good one.
	Warnings []string `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// SignalState is how one obtainability signal resolved. It is the one shared
// answer to "did this signal return data?", so a lookup that FAILED is never
// rendered or serialized as one that returned zero: an unknown footprint is not
// "not offered" (#274), an unreadable quota is not "quota is 0" (#271), and a
// failed Capacity Block query is not "no offerings" (#272). Every signal that
// can be unknown carries a state field of this type, set by its record* method
// and, on failure, by [signalUnavailable].
type SignalState string

const (
	// SignalCollected means the lookup returned data, which may legitimately be
	// zero or empty.
	SignalCollected SignalState = "collected"
	// SignalUnavailable means the lookup failed (AccessDenied, throttling, a
	// missing grant, ...), so the value is unknown — not zero. The reason is in
	// Warnings.
	SignalUnavailable SignalState = "unavailable"
	// SignalIneligible means AWS answered definitively that the signal does not
	// apply (today only Capacity Blocks: the type is not sold that way in the
	// region). Not an error, and no amount of waiting changes it.
	SignalIneligible SignalState = "ineligible"
)

// Known reports whether the signal returned data. Everything else — failed,
// ineligible, or never set — must not be presented as a value.
func (s SignalState) Known() bool { return s == SignalCollected }

// signalUnavailable is the one way a signal is marked unknown: it sets the state
// and returns the warning to record, worded "<what> unavailable: <err>". Sharing
// it keeps every signal's failure path identical — state, wording and the rule
// that the value is left unset rather than zeroed.
func signalUnavailable(state *SignalState, what string, err error) string {
	*state = SignalUnavailable
	return fmt.Sprintf("%s unavailable: %v", what, err)
}

// CapacityBlockState is the outcome of the Capacity Block offerings query. It is
// a [SignalState]; the aliases below name its three outcomes.
type CapacityBlockState = SignalState

const (
	// CapacityBlockCounted means the query ran and CapacityBlockOfferings holds
	// the count (which may be zero).
	CapacityBlockCounted = SignalCollected
	// CapacityBlockIneligible means AWS reported the type is not a Capacity
	// Block type in this region ([ErrCapacityBlockIneligible]). No amount of
	// waiting changes it, and it is not an error.
	CapacityBlockIneligible = SignalIneligible
	// CapacityBlockUnavailable means the query failed (AccessDenied,
	// throttling, ...), so nothing is known; the error is in Warnings.
	CapacityBlockUnavailable = SignalUnavailable
)

// recordCapacityBlocks stores the result of the Capacity Block offerings query
// and returns the warning to record, or "" when there is none. An ineligible
// type is a clean answer, not a warning: printing AWS's raw
// InvalidParameterValue text for it read as a failure (#272). Every other
// error is a real gap and stays a warning. The caller holds any lock guarding o.
func (o *Obtainability) recordCapacityBlocks(offerings int, err error) string {
	switch {
	case err == nil:
		o.CapacityBlockState = CapacityBlockCounted
		o.CapacityBlockOfferings = &offerings
		o.CapacityBlockWindowHours = obtainabilityCapacityBlockHours
		return ""
	case errors.Is(err, ErrCapacityBlockIneligible):
		o.CapacityBlockState = CapacityBlockIneligible
		return ""
	default:
		return signalUnavailable(&o.CapacityBlockState, "capacity block offerings", err)
	}
}

// recordOfferedAZs stores the result of the offered-AZ lookup and returns the
// warning to record, or "" when there is none. A failed lookup leaves OfferedAZs
// nil and the state unavailable: the footprint is unknown, and rendering it as
// "0 — not offered in this region" told users a throttled or denied
// DescribeInstanceTypeOfferings meant the type doesn't exist there (#274). The
// caller holds any lock guarding o.
func (o *Obtainability) recordOfferedAZs(offered []string, err error) string {
	if err != nil {
		o.OfferedAZs = nil
		return signalUnavailable(&o.OfferedAZsState, "offered AZs", err)
	}
	// Deduplicate before counting. DescribeInstanceTypeOfferings returns one row
	// per (type, location), so anything that widens the match — or a backend that
	// doesn't honour the instance-type filter — yields the same AZ repeatedly.
	// Since this count is rendered against a region-AZ denominator ("1 of 6"), a
	// duplicate would print a nonsense ratio like "24 of 3".
	offered = uniqueSorted(offered)
	SortZoneNames(offered)
	o.OfferedAZs = offered
	o.OfferedAZsState = SignalCollected
	return ""
}

// SpotPlacement is one AZ's spot placement score. Both identifiers are kept: the
// name (us-east-1a) is what every other truffle command prints and what a user
// puts in a launch request, while the ID (use1-az2) is what AWS actually returned
// and is stable across accounts — the same name maps to different physical AZs in
// different accounts, so a support conversation or a cross-account comparison
// needs the ID.
type SpotPlacement struct {
	// AZ is the availability zone name (us-east-1a), or the ID when it could not
	// be resolved to a name.
	AZ string `json:"az" yaml:"az"`
	// AZID is the account-specific zone ID AWS returned (use1-az2).
	AZID string `json:"az_id,omitempty" yaml:"az_id,omitempty"`
	// Score is 1–10, higher being more likely to be fulfilled. It is a relative
	// likelihood, not a guarantee or a percentage.
	Score int `json:"score" yaml:"score"`
}

// BestSpotPlacement returns the highest-scoring AZ. ok is false when no score is
// available. SpotPlacements is already sorted best-first, so this is the head.
func (o *Obtainability) BestSpotPlacement() (p SpotPlacement, ok bool) {
	if len(o.SpotPlacements) == 0 {
		return SpotPlacement{}, false
	}
	return o.SpotPlacements[0], true
}

// OfferedAZsKnown reports whether the offered-AZ footprint was actually
// observed, so an empty OfferedAZs can be read as "not offered in this region".
// A failed lookup is unknown, never zero (#274). For a value built without a
// state (not by [Client.Obtainability]) only a non-empty list counts as known:
// absent evidence is never promoted to a hard negative.
func (o *Obtainability) OfferedAZsKnown() bool {
	if o.OfferedAZsState != "" {
		return o.OfferedAZsState.Known()
	}
	return len(o.OfferedAZs) > 0
}

// QuotaHeadroomKnown reports whether the On-Demand quota was actually read, so
// a zero OnDemandQuotaHeadroom can be read as "no headroom". An unreadable quota
// is unknown, never zero (#271). For a value built without a state, the presence
// of OnDemandQuotaHeadroom decides.
func (o *Obtainability) QuotaHeadroomKnown() bool {
	if o.QuotaHeadroomState != "" {
		return o.QuotaHeadroomState.Known()
	}
	return o.OnDemandQuotaHeadroom != nil
}

// recordQuotaHeadroom stores the On-Demand quota headroom for family from a
// [quotas.Client.GetQuotas] result and returns the warning to record, or "".
//
// Since #167 GetQuotas does not fail when one family's GetServiceQuota does: it
// leaves the family ABSENT from OnDemand and records the error in
// OnDemandErrors. A one-value map read turned that absence into a limit of 0,
// so `truffle available` told a user missing an IAM grant that their quota was
// zero (#271). The two-value read here keeps it unknown. The caller holds any
// lock guarding o.
func (o *Obtainability) recordQuotaHeadroom(info *quotas.QuotaInfo, family quotas.QuotaFamily, err error) string {
	if err != nil {
		return signalUnavailable(&o.QuotaHeadroomState, "quota headroom", err)
	}
	limit, ok := info.OnDemand[family]
	if !ok {
		cause := info.LookupError(family, false)
		if cause == nil {
			cause = errors.New("no value was returned")
		}
		code := o.QuotaCode
		if code == "" {
			code = "no quota code known"
		}
		return signalUnavailable(&o.QuotaHeadroomState, "quota headroom",
			fmt.Errorf("the On-Demand %s vCPU quota (%s) could not be read — limit unknown, not zero: %w", family, code, cause))
	}
	headroom := int(limit - info.Usage[family])
	if headroom < 0 {
		headroom = 0
	}
	o.OnDemandQuotaHeadroom = &headroom
	o.QuotaHeadroomState = SignalCollected
	return ""
}

// InstanceHeadroom converts the vCPU quota headroom into how many instances of
// this type could be launched under it. ok is false when either the headroom or
// the per-instance vCPU count is unknown.
func (o *Obtainability) InstanceHeadroom() (count int, ok bool) {
	if o.OnDemandQuotaHeadroom == nil || o.VCPUsPerInstance <= 0 {
		return 0, false
	}
	return *o.OnDemandQuotaHeadroom / o.VCPUsPerInstance, true
}

// obtainabilityCapacityBlockHours is the Capacity Block window probed for the
// offering count: the smallest valid duration (1 day). A short window is the right
// default here — the question is "is there any inventory at all", and a longer
// duration is strictly harder to fill, so 24h gives the most generous honest
// answer.
const obtainabilityCapacityBlockHours = 24

// Obtainability gathers the availability signals for one instance type in one
// region: spot placement scores per AZ, the offered-AZ footprint, On-Demand quota
// headroom, and purchasable Capacity Block offerings. All are collected
// concurrently.
//
// It is intentionally best-effort per signal. Any one source can be denied by an
// SCP or missing credentials, and a partial answer is far more useful than none —
// so a failed signal is recorded in Warnings and left zero rather than aborting
// the call. Only a failure to resolve the type at all is returned as an error.
//
// Note on probing: do NOT substitute RunInstances --dry-run for this. DryRun
// validates permissions and parameters only — neither capacity nor quota — so it
// returns "would have succeeded" for a type with a 1/10 spot score, a single-AZ
// footprint and zero capacity-block offerings (verified 2026-07-27 against
// p6-b200.48xlarge, p5.4xlarge and g7e.4xlarge alike). It cannot answer this
// question and will mislead anyone who reaches for it.
func (c *Client) Obtainability(ctx context.Context, instanceType, region string) (*Obtainability, error) {
	if strings.TrimSpace(instanceType) == "" {
		return nil, fmt.Errorf("obtainability: instanceType is required")
	}
	if strings.TrimSpace(region) == "" {
		return nil, fmt.Errorf("obtainability: region is required")
	}

	out := &Obtainability{InstanceType: instanceType, Region: region}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	warn := func(format string, args ...any) {
		mu.Lock()
		out.Warnings = append(out.Warnings, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	// 1. Spot placement scores (the only forward-looking signal AWS exposes).
	wg.Add(1)
	go func() {
		defer wg.Done()
		placements, err := c.spotPlacementScores(ctx, instanceType, region)
		if err != nil {
			// Commonly denied by SCP or unavailable to the account — a warning, not
			// a failure, but never silent.
			warn("spot placement scores unavailable: %v", err)
			return
		}
		mu.Lock()
		out.SpotPlacements = placements
		mu.Unlock()
	}()

	// 2. Offered-AZ footprint (a hard constraint AWS already answers).
	wg.Add(1)
	go func() {
		defer wg.Done()
		offered, err := c.getAvailabilityZones(ctx, region, instanceType)
		mu.Lock()
		warning := out.recordOfferedAZs(offered, err)
		mu.Unlock()
		if warning != "" {
			warn("%s", warning)
			return
		}
		total, err := c.countRegionAZs(ctx, region)
		if err != nil {
			warn("region AZ count unavailable: %v", err)
		}
		mu.Lock()
		out.TotalAZs = total
		mu.Unlock()
	}()

	// 3. On-Demand quota headroom (the gate that blocks most accounts).
	wg.Add(1)
	go func() {
		defer wg.Done()
		family := quotas.GetQuotaFamily(instanceType)
		// The single source of truth for quota codes (#167): an unmapped family
		// gets no code rather than another family's.
		code, _ := quotas.QuotaCodeFor(family, false)
		mu.Lock()
		out.QuotaFamily, out.QuotaCode = string(family), code
		mu.Unlock()

		qc := quotas.NewClientFromConfig(c.cfg)
		info, err := qc.GetQuotas(ctx, region)
		if err == nil && !info.CredentialsAvailable {
			warn("quota headroom is an estimate (no credentials)")
		}
		mu.Lock()
		warning := out.recordQuotaHeadroom(info, family, err)
		mu.Unlock()
		if warning != "" {
			warn("%s", warning)
		}
	}()

	// 4. vCPU count, so headroom converts to a usable instance count. Taken from
	// DescribeInstanceTypes rather than parsed from the type name: the name-based
	// estimate in pkg/quotas is a fallback for usage accounting, and guessing here
	// would silently misreport headroom for any type whose size suffix doesn't map
	// linearly (p6-b200.48xlarge, the metal sizes).
	wg.Add(1)
	go func() {
		defer wg.Done()
		vcpus, err := c.instanceTypeVCPUs(ctx, region, instanceType)
		if err != nil {
			warn("vCPU count unavailable: %v", err)
			return
		}
		mu.Lock()
		out.VCPUsPerInstance = vcpus
		mu.Unlock()
	}()

	// 5. Purchasable Capacity Block offerings. Probed at count=1, the most
	// permissive ask: a larger count silently gates results, so asking for 8 and
	// getting nothing reads as "no capacity exists" when the truth is "not that many
	// at once" (#109).
	wg.Add(1)
	go func() {
		defer wg.Done()
		offerings, err := c.GetCapacityBlockOfferings(ctx, []string{region}, CapacityBlockOfferingOptions{
			InstanceType:          instanceType,
			InstanceCount:         1,
			CapacityDurationHours: obtainabilityCapacityBlockHours,
		})
		mu.Lock()
		warning := out.recordCapacityBlocks(len(offerings), err)
		mu.Unlock()
		if warning != "" {
			warn("%s", warning)
		}
	}()

	wg.Wait()

	sortSpotPlacements(out.SpotPlacements)

	return out, nil
}

// uniqueSorted returns the distinct values of in, sorted.
func uniqueSorted(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// sortSpotPlacements orders placements best-score-first, tie-breaking on AZ name
// so the output is deterministic (AWS does not guarantee a response order).
func sortSpotPlacements(placements []SpotPlacement) {
	sort.SliceStable(placements, func(i, j int) bool {
		if placements[i].Score != placements[j].Score {
			return placements[i].Score > placements[j].Score
		}
		return placements[i].AZ < placements[j].AZ
	})
}

// instanceTypeVCPUs returns the authoritative default vCPU count for a type.
func (c *Client) instanceTypeVCPUs(ctx context.Context, region, instanceType string) (int, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	out, err := client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
	})
	if err != nil {
		return 0, err
	}
	for _, it := range out.InstanceTypes {
		if it.VCpuInfo != nil && it.VCpuInfo.DefaultVCpus != nil {
			return int(*it.VCpuInfo.DefaultVCpus), nil
		}
	}
	return 0, fmt.Errorf("no vCPU info for %s", instanceType)
}

// spotPlacementScores calls GetSpotPlacementScores for one type in one region and
// maps the AZ IDs it returns to AZ names.
//
// Two API requirements are load-bearing: SingleAvailabilityZone must be true to
// get a per-AZ breakdown (otherwise AWS scores the region as a whole), and
// RegionNames must be set. The response carries AvailabilityZoneId
// (use1-az2) rather than a name, and those IDs are account-specific — the same
// physical AZ has a different name in different accounts — so they must be mapped
// through DescribeAvailabilityZones to be comparable with anything else truffle
// prints (#108).
func (c *Client) spotPlacementScores(ctx context.Context, instanceType, region string) ([]SpotPlacement, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	out, err := client.GetSpotPlacementScores(ctx, &ec2.GetSpotPlacementScoresInput{
		InstanceTypes:          []string{instanceType},
		RegionNames:            []string{region},
		SingleAvailabilityZone: aws.Bool(true),
		TargetCapacity:         aws.Int32(1),
		TargetCapacityUnitType: types.TargetCapacityUnitTypeUnits,
	})
	if err != nil {
		return nil, err
	}

	// A name lookup failure is not fatal: the score is reported against the raw ID
	// rather than dropped, since the region's spot outlook is useful even when the
	// AZ can't be named.
	idToName, _ := c.azIDToName(ctx, region)

	placements := make([]SpotPlacement, 0, len(out.SpotPlacementScores))
	for _, s := range out.SpotPlacementScores {
		if s.Score == nil || s.AvailabilityZoneId == nil {
			continue
		}
		id := *s.AvailabilityZoneId
		name := id
		if n, ok := idToName[id]; ok {
			name = n
		}
		placements = append(placements, SpotPlacement{AZ: name, AZID: id, Score: int(*s.Score)})
	}
	return placements, nil
}

// azIDToName maps account-specific AZ IDs (use1-az2) to AZ names (us-east-1a).
func (c *Client) azIDToName(ctx context.Context, region string) (map[string]string, error) {
	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	out, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(out.AvailabilityZones))
	for _, az := range out.AvailabilityZones {
		if az.ZoneId != nil && az.ZoneName != nil {
			m[*az.ZoneId] = *az.ZoneName
		}
	}
	return m, nil
}

// countRegionAZs returns how many AZs the region exposes, giving the offered-AZ
// count a denominator.
func (c *Client) countRegionAZs(ctx context.Context, region string) (int, error) {
	m, err := c.azIDToName(ctx, region)
	if err != nil {
		return 0, err
	}
	return len(m), nil
}

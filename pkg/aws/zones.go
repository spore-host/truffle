package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// Zone type strings as reported by DescribeAvailabilityZones' ZoneType field.
// These are the authoritative classification signal — truffle keys Local Zone
// awareness off these values rather than guessing from the zone name (#163).
const (
	ZoneTypeAvailabilityZone = "availability-zone"
	ZoneTypeLocalZone        = "local-zone"
	ZoneTypeWavelengthZone   = "wavelength-zone"
)

// ZoneInfo is the authoritative classification of a single zone, sourced from
// DescribeAvailabilityZones rather than inferred from the zone name (#163/#164).
// A caller can distinguish a standard in-region Availability Zone from a Local
// Zone or Wavelength Zone, tell whether a zone exists but isn't opted into, and
// find the region AZ a Local/Wavelength zone hangs off of.
type ZoneInfo struct {
	ZoneName       string `json:"zone_name" yaml:"zone_name"`                                   // e.g. "us-east-1a" or "us-east-1-bos-1a"
	ZoneID         string `json:"zone_id,omitempty" yaml:"zone_id,omitempty"`                   // e.g. "use1-az1"
	ZoneType       string `json:"zone_type" yaml:"zone_type"`                                   // availability-zone / local-zone / wavelength-zone
	OptInStatus    string `json:"opt_in_status,omitempty" yaml:"opt_in_status,omitempty"`       // opt-in-not-required / opted-in / not-opted-in
	ParentZoneName string `json:"parent_zone_name,omitempty" yaml:"parent_zone_name,omitempty"` // in-region AZ a Local/Wavelength zone attaches to
	GroupName      string `json:"group_name,omitempty" yaml:"group_name,omitempty"`             // network border group
	Region         string `json:"region" yaml:"region"`                                         // AWS region this zone belongs to
}

// IsExtended reports whether this zone is a Local Zone or Wavelength Zone — an
// "extended" location outside the region's standard in-region AZs — classified
// by its authoritative ZoneType. An empty or unrecognized ZoneType (e.g. when
// DescribeAvailabilityZones was unavailable, or a mock/older API didn't
// populate the field) is treated as a standard Availability Zone, so callers
// degrade to today's "everything is a normal AZ" behavior rather than
// mislabeling.
func (z ZoneInfo) IsExtended() bool {
	return isExtendedZoneType(z.ZoneType)
}

// Label returns a short human-readable tag for a non-standard zone
// ("Local Zone" / "Wavelength"), or "" for a standard Availability Zone.
func (z ZoneInfo) Label() string {
	switch z.ZoneType {
	case ZoneTypeLocalZone:
		return "Local Zone"
	case ZoneTypeWavelengthZone:
		return "Wavelength"
	default:
		return ""
	}
}

// isExtendedZoneType is the pure classification the name-length heuristic in
// cmd/spot.go used to approximate (#163): a zone is "extended" iff its
// DescribeAvailabilityZones ZoneType says local-zone or wavelength-zone, never
// because of how long its name is.
func isExtendedZoneType(zoneType string) bool {
	switch zoneType {
	case ZoneTypeLocalZone, ZoneTypeWavelengthZone:
		return true
	default:
		return false
	}
}

// ZoneInfos returns every zone in the region keyed by zone name, classified by
// the authoritative DescribeAvailabilityZones ZoneType. It requests
// AllAvailabilityZones=true so Local/Wavelength zones that exist but the
// account hasn't opted into are visible too (a common reason "no capacity" is
// really "not enabled"). The result is memoized per region on the Client — one
// DescribeAvailabilityZones call per region for the client's lifetime — since
// zone topology is effectively static for a run.
func (c *Client) ZoneInfos(ctx context.Context, region string) (map[string]ZoneInfo, error) {
	c.zoneCacheMu.Lock()
	if c.zoneCache != nil {
		if cached, ok := c.zoneCache[region]; ok {
			c.zoneCacheMu.Unlock()
			return cached, nil
		}
	}
	c.zoneCacheMu.Unlock()

	cfg := c.cfg
	cfg.Region = region
	client := ec2.NewFromConfig(cfg)

	// AllAvailabilityZones surfaces zones the account has not opted into (Local
	// and Wavelength zones default to not-opted-in), so we can report that a zone
	// exists-but-isn't-enabled rather than it being invisible.
	out, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{
		AllAvailabilityZones: boolPtr(true),
	})
	if err != nil {
		return nil, fmt.Errorf("describe availability zones in %s: %w", region, err)
	}

	zones := make(map[string]ZoneInfo, len(out.AvailabilityZones))
	for _, az := range out.AvailabilityZones {
		info := ZoneInfo{
			ZoneName:       valueOrZero(az.ZoneName),
			ZoneID:         valueOrZero(az.ZoneId),
			ZoneType:       valueOrZero(az.ZoneType),
			OptInStatus:    string(az.OptInStatus),
			ParentZoneName: valueOrZero(az.ParentZoneName),
			GroupName:      valueOrZero(az.GroupName),
			Region:         region,
		}
		if info.ZoneName != "" {
			zones[info.ZoneName] = info
		}
	}

	c.zoneCacheMu.Lock()
	if c.zoneCache == nil {
		c.zoneCache = make(map[string]map[string]ZoneInfo)
	}
	c.zoneCache[region] = zones
	c.zoneCacheMu.Unlock()

	return zones, nil
}

// LookupZone returns the classification for a single zone in a region. ok is
// false when the zone name isn't present in the region's
// DescribeAvailabilityZones result.
func (c *Client) LookupZone(ctx context.Context, region, zoneName string) (ZoneInfo, bool, error) {
	zones, err := c.ZoneInfos(ctx, region)
	if err != nil {
		return ZoneInfo{}, false, err
	}
	info, ok := zones[zoneName]
	return info, ok, nil
}

// IsLocalZone reports whether zoneName in region is a Local Zone or Wavelength
// Zone, per its authoritative ZoneType (DescribeAvailabilityZones) — replacing
// the brittle name-length heuristic (#163).
//
// It degrades safely: if the DescribeAvailabilityZones call fails it returns
// (false, err) — the boolean treats the zone as a standard AZ so an offline or
// permission-denied run behaves as truffle did before this classification
// existed, while the error is still available to a caller that wants to log it.
// A zone that simply isn't found (nil error) is likewise reported as not-local.
func (c *Client) IsLocalZone(ctx context.Context, region, zoneName string) (bool, error) {
	info, ok, err := c.LookupZone(ctx, region, zoneName)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return info.IsExtended(), nil
}

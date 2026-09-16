package aws

import (
	"context"
	"testing"

	"github.com/spore-host/truffle/pkg/testutil"
)

// oldLengthHeuristic reproduces the pre-#163 Local Zone guess from cmd/spot.go:
// a zone was treated as a *standard* AZ iff its name was no longer than
// region+one letter, and "local" otherwise. It exists only so the tests below
// can demonstrate exactly where that heuristic diverges from the authoritative
// ZoneType classification.
func oldLengthHeuristic(region, zoneName string) (isLocal bool) {
	return len(zoneName) > len(region)+1
}

// TestZoneClassificationByType is the pure, AWS-free classification test that
// replaces the brittle name-length heuristic (#163): classification is driven
// solely by the authoritative DescribeAvailabilityZones ZoneType, never by how
// long the zone name is.
func TestZoneClassificationByType(t *testing.T) {
	cases := []struct {
		name         string
		zone         ZoneInfo
		wantExtended bool
		wantLabel    string
	}{
		{
			name:         "standard availability zone",
			zone:         ZoneInfo{ZoneName: "us-east-1a", ZoneType: ZoneTypeAvailabilityZone, Region: "us-east-1"},
			wantExtended: false,
			wantLabel:    "",
		},
		{
			name:         "local zone",
			zone:         ZoneInfo{ZoneName: "us-east-1-bos-1a", ZoneType: ZoneTypeLocalZone, Region: "us-east-1"},
			wantExtended: true,
			wantLabel:    "Local Zone",
		},
		{
			name:         "wavelength zone",
			zone:         ZoneInfo{ZoneName: "us-east-1-wl1-bos-wlz-1", ZoneType: ZoneTypeWavelengthZone, Region: "us-east-1"},
			wantExtended: true,
			wantLabel:    "Wavelength",
		},
		{
			name:         "empty ZoneType falls back to standard AZ",
			zone:         ZoneInfo{ZoneName: "us-east-1a", ZoneType: "", Region: "us-east-1"},
			wantExtended: false,
			wantLabel:    "",
		},
		{
			name:         "unknown ZoneType falls back to standard AZ",
			zone:         ZoneInfo{ZoneName: "us-east-1a", ZoneType: "outpost-zone", Region: "us-east-1"},
			wantExtended: false,
			wantLabel:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.zone.IsExtended(); got != tc.wantExtended {
				t.Errorf("IsExtended() = %v, want %v (ZoneType=%q)", got, tc.wantExtended, tc.zone.ZoneType)
			}
			if got := tc.zone.Label(); got != tc.wantLabel {
				t.Errorf("Label() = %q, want %q", got, tc.wantLabel)
			}
		})
	}
}

// TestZoneClassificationBeatsLengthHeuristic pins the concrete cases the old
// name-length heuristic got wrong and the ZoneType classifier gets right (#163).
// Both zones are synthetic — their names are deliberately chosen so their length
// points the opposite way from their real ZoneType — which is the whole point:
// truffle now classifies by ZoneType, so the name's length is irrelevant.
func TestZoneClassificationBeatsLengthHeuristic(t *testing.T) {
	const region = "us-east-1"

	// A Local Zone whose name is SHORT (same length as a standard AZ). The old
	// heuristic (len 10 is not > len(region)+1 == 10) calls it a standard AZ;
	// ZoneType says local-zone.
	shortLocal := ZoneInfo{ZoneName: "us-east-1x", ZoneType: ZoneTypeLocalZone, Region: region}
	if oldLengthHeuristic(region, shortLocal.ZoneName) {
		t.Fatalf("test premise broken: length heuristic already flags %q as local", shortLocal.ZoneName)
	}
	if !shortLocal.IsExtended() {
		t.Errorf("short-named local zone %q: IsExtended() = false, want true (ZoneType classification must win over name length)", shortLocal.ZoneName)
	}

	// A standard AZ whose name is LONG. The old heuristic (len 15 > 10) calls it
	// a local zone; ZoneType says availability-zone.
	longStandard := ZoneInfo{ZoneName: "us-east-1-extra", ZoneType: ZoneTypeAvailabilityZone, Region: region}
	if !oldLengthHeuristic(region, longStandard.ZoneName) {
		t.Fatalf("test premise broken: length heuristic does not flag %q as local", longStandard.ZoneName)
	}
	if longStandard.IsExtended() {
		t.Errorf("long-named standard AZ %q: IsExtended() = true, want false (ZoneType classification must win over name length)", longStandard.ZoneName)
	}
}

// TestZoneInfos_Substrate exercises the real DescribeAvailabilityZones path
// against the Substrate emulator: it seeds standard AZs only, so every zone must
// classify as a standard AZ (none extended) and the result must be memoized.
func TestZoneInfos_Substrate(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()
	c := NewClientFromConfig(env.AWSConfig)

	zones, err := c.ZoneInfos(ctx, "us-east-1")
	if err != nil {
		t.Fatalf("ZoneInfos() error = %v", err)
	}
	if len(zones) == 0 {
		t.Fatal("ZoneInfos() returned 0 zones, want >= 1")
	}
	for name, info := range zones {
		if info.IsExtended() {
			t.Errorf("zone %q classified as extended; Substrate seeds only standard AZs", name)
		}
		if info.Region != "us-east-1" {
			t.Errorf("zone %q Region = %q, want us-east-1", name, info.Region)
		}
	}

	// Second call must hit the memoized cache and return the same map.
	again, err := c.ZoneInfos(ctx, "us-east-1")
	if err != nil {
		t.Fatalf("ZoneInfos() second call error = %v", err)
	}
	if len(again) != len(zones) {
		t.Errorf("cached ZoneInfos() len = %d, want %d", len(again), len(zones))
	}
}

// TestIsLocalZone_Substrate verifies IsLocalZone over the real API path: a
// seeded standard AZ is not local, and an unknown zone name degrades to
// (false, nil) rather than erroring.
func TestIsLocalZone_Substrate(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()
	c := NewClientFromConfig(env.AWSConfig)

	isLocal, err := c.IsLocalZone(ctx, "us-east-1", "us-east-1a")
	if err != nil {
		t.Fatalf("IsLocalZone() error = %v", err)
	}
	if isLocal {
		t.Error("IsLocalZone(us-east-1a) = true, want false (standard AZ)")
	}

	// A zone the region doesn't have: not found → treated as not-local, no error.
	isLocal, err = c.IsLocalZone(ctx, "us-east-1", "us-east-1-nope-1a")
	if err != nil {
		t.Fatalf("IsLocalZone(unknown) error = %v, want nil", err)
	}
	if isLocal {
		t.Error("IsLocalZone(unknown zone) = true, want false")
	}
}

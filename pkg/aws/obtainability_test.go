package aws

import (
	"context"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/testutil"
)

// Tests for the #108 obtainability signals.
//
// Two things are worth knowing about the shape of these tests. First, the
// Substrate emulator implements DescribeAvailabilityZones, DescribeInstanceTypes
// and DescribeInstanceTypeOfferings but NOT GetSpotPlacementScores or
// DescribeCapacityBlockOfferings, so the emulator-backed test exercises the
// partial-answer path — which is the one that actually matters in production, since
// GetSpotPlacementScores is commonly denied by SCP. Second, the pure functions
// (ranking, headroom conversion) are tested directly on the struct, because those
// are what a consumer reads and they must not depend on any API being reachable.

// TestObtainability_RequiresTypeAndRegion pins the two arguments that cannot be
// defaulted. Both signals are region-specific and type-specific, so an empty value
// would silently produce a meaningless answer rather than an error.
func TestObtainability_RequiresTypeAndRegion(t *testing.T) {
	c := NewClientFromConfig(testutil.SubstrateServer(t).AWSConfig)
	ctx := context.Background()

	if _, err := c.Obtainability(ctx, "", "us-east-1"); err == nil {
		t.Error("empty instanceType returned no error")
	}
	if _, err := c.Obtainability(ctx, "p5.48xlarge", ""); err == nil {
		t.Error("empty region returned no error")
	}
	if _, err := c.Obtainability(ctx, "   ", "us-east-1"); err == nil {
		t.Error("whitespace instanceType returned no error")
	}
}

// TestObtainability_PartialAnswerNotAnError is the central contract: when a signal
// source is unavailable the call still returns a result, and the gap is recorded in
// Warnings. Reporting an error instead would make the command useless in the very
// common case of an SCP denying GetSpotPlacementScores, and leaving the gap silent
// would let an absent signal read as a good one.
func TestObtainability_PartialAnswerNotAnError(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	obt, err := c.Obtainability(context.Background(), "m5.large", "us-east-1")
	if err != nil {
		t.Fatalf("Obtainability returned an error for a partially-answerable query: %v", err)
	}
	if obt == nil {
		t.Fatal("Obtainability returned nil result with nil error")
	}
	if obt.InstanceType != "m5.large" || obt.Region != "us-east-1" {
		t.Errorf("echoed query = %s/%s, want m5.large/us-east-1", obt.InstanceType, obt.Region)
	}
	// The emulator serves no GetSpotPlacementScores, so that gap must be visible.
	if len(obt.Warnings) == 0 {
		t.Error("no warnings recorded despite unavailable signals — a missing signal must never be silent")
	}
	if _, ok := obt.BestSpotPlacement(); ok {
		t.Error("BestSpotPlacement reported a score the emulator never returned")
	}
}

// TestObtainability_QuotaCodeIsIdentified verifies the quota a user would file an
// increase against is named even when the quota value itself is unavailable. A
// headroom of 0 with no quota code is an unactionable dead end.
func TestObtainability_QuotaCodeIsIdentified(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	for _, tc := range []struct {
		instanceType string
		wantFamily   string
		wantCode     string
	}{
		{"p5.48xlarge", "P", "L-417A185B"},
		{"g6.xlarge", "G", "L-DB2E81BA"},
		{"m5.large", "Standard", "L-1216C47A"},
	} {
		obt, err := c.Obtainability(context.Background(), tc.instanceType, "us-east-1")
		if err != nil {
			t.Fatalf("%s: %v", tc.instanceType, err)
		}
		if obt.QuotaFamily != tc.wantFamily {
			t.Errorf("%s: quota family = %q, want %q", tc.instanceType, obt.QuotaFamily, tc.wantFamily)
		}
		if obt.QuotaCode != tc.wantCode {
			t.Errorf("%s: quota code = %q, want %q", tc.instanceType, obt.QuotaCode, tc.wantCode)
		}
	}
}

// TestObtainability_ReadsVCPUsFromAPI verifies the vCPU count comes from
// DescribeInstanceTypes rather than being parsed from the type name. Guessing from
// the size suffix would misreport headroom for any type whose suffix doesn't map
// linearly, which is most accelerator and metal types.
func TestObtainability_ReadsVCPUsFromAPI(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	obt, err := c.Obtainability(context.Background(), "m5.large", "us-east-1")
	if err != nil {
		t.Fatalf("Obtainability: %v", err)
	}
	if obt.VCPUsPerInstance != 2 {
		t.Errorf("vCPUs = %d, want 2 for m5.large (from DescribeInstanceTypes)", obt.VCPUsPerInstance)
	}
}

// TestBestSpotPlacement verifies the best AZ wins regardless of the order AWS
// returned the scores in, and that ties break deterministically on AZ name. AWS
// documents no response ordering, so without this the reported "best AZ" could
// change between identical calls.
func TestBestSpotPlacement(t *testing.T) {
	o := &Obtainability{SpotPlacements: []SpotPlacement{
		{AZ: "us-east-1c", AZID: "use1-az4", Score: 3},
		{AZ: "us-east-1a", AZID: "use1-az2", Score: 7},
		{AZ: "us-east-1b", AZID: "use1-az1", Score: 7},
	}}
	sortSpotPlacements(o.SpotPlacements)

	best, ok := o.BestSpotPlacement()
	if !ok {
		t.Fatal("BestSpotPlacement reported no score")
	}
	if best.Score != 7 {
		t.Errorf("score = %d, want 7", best.Score)
	}
	if best.AZ != "us-east-1a" {
		t.Errorf("az = %q, want us-east-1a (lowest name among the tied 7s)", best.AZ)
	}
	if best.AZID != "use1-az2" {
		t.Errorf("az id = %q, want use1-az2 — the ID must travel with the name", best.AZID)
	}

	if _, ok := (&Obtainability{}).BestSpotPlacement(); ok {
		t.Error("BestSpotPlacement on an empty result reported ok=true")
	}
}

// TestInstanceHeadroom verifies vCPU headroom converts to an instance count, and
// that an unknown input yields ok=false rather than a confident zero. "0 instances"
// and "unknown" are different answers and must not be conflated.
func TestInstanceHeadroom(t *testing.T) {
	headroom := func(n int) *int { return &n }

	for _, tc := range []struct {
		name      string
		o         Obtainability
		wantCount int
		wantOK    bool
	}{
		{"exact fit", Obtainability{OnDemandQuotaHeadroom: headroom(384), VCPUsPerInstance: 192}, 2, true},
		{"rounds down", Obtainability{OnDemandQuotaHeadroom: headroom(200), VCPUsPerInstance: 192}, 1, true},
		{"not even one", Obtainability{OnDemandQuotaHeadroom: headroom(96), VCPUsPerInstance: 192}, 0, true},
		{"zero quota", Obtainability{OnDemandQuotaHeadroom: headroom(0), VCPUsPerInstance: 192}, 0, true},
		{"unknown headroom", Obtainability{VCPUsPerInstance: 192}, 0, false},
		{"unknown vcpus", Obtainability{OnDemandQuotaHeadroom: headroom(384)}, 0, false},
	} {
		count, ok := tc.o.InstanceHeadroom()
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
		}
		if ok && count != tc.wantCount {
			t.Errorf("%s: count = %d, want %d", tc.name, count, tc.wantCount)
		}
	}
}

// TestObtainability_CapacityBlockCountHasAWindow verifies the offering count is
// never reported without the duration it was measured over. A bare "0 offerings"
// reads as "never available"; "0 offerings (24h window)" is the honest claim.
func TestObtainability_CapacityBlockCountHasAWindow(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	obt, err := c.Obtainability(context.Background(), "p5.48xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("Obtainability: %v", err)
	}
	if obt.CapacityBlockOfferings != nil && obt.CapacityBlockWindowHours == 0 {
		t.Error("capacity block count reported with no window — the number needs a stated scope")
	}
	if obt.CapacityBlockOfferings == nil {
		// The emulator doesn't serve DescribeCapacityBlockOfferings; the gap must be
		// named rather than left as an implicit zero.
		if !hasWarningAbout(obt.Warnings, "capacity block") {
			t.Errorf("unavailable capacity block signal not warned about; warnings = %v", obt.Warnings)
		}
	}
}

func hasWarningAbout(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(strings.ToLower(w), substr) {
			return true
		}
	}
	return false
}

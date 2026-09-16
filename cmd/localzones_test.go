package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// TestFindLocalZonesRejectsSkipAZs: --local-zones needs AZ data, so combining it
// with --skip-azs is rejected up front (before any AWS call) with a clear error
// (#164).
func TestFindLocalZonesRejectsSkipAZs(t *testing.T) {
	prevLocal, prevSkip := findLocalZones, findSkipAZs
	defer func() { findLocalZones, findSkipAZs = prevLocal, prevSkip }()

	findLocalZones, findSkipAZs = true, true
	err := runFind(findCmd, []string{"graviton"})
	if err == nil {
		t.Fatal("expected --local-zones + --skip-azs to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "skip-azs") {
		t.Errorf("error should mention the conflicting flag, got: %v", err)
	}
}

// TestFilterToLocalZones covers the opt-in --local-zones targeting filter on
// find/az (#164): a result with mixed AZs keeps only its Local/Wavelength AZs,
// a result with no edge AZ is dropped entirely, and an all-edge result is
// unchanged.
func TestFilterToLocalZones(t *testing.T) {
	results := []aws.InstanceTypeResult{
		// Mixed: standard AZs plus one Local Zone.
		{InstanceType: "m5.large", Region: "us-east-1", AvailableAZs: []string{"us-east-1a", "us-east-1b", "us-east-1-bos-1a"}},
		// No edge AZ at all — must be dropped.
		{InstanceType: "c6i.large", Region: "us-east-1", AvailableAZs: []string{"us-east-1a", "us-east-1c"}},
		// Entirely edge zones — kept as-is.
		{InstanceType: "g4dn.xlarge", Region: "us-west-2", AvailableAZs: []string{"us-west-2-lax-1a", "us-west-2-lax-1b"}},
	}
	localZones := map[string]bool{
		"us-east-1-bos-1a": true,
		"us-west-2-lax-1a": true,
		"us-west-2-lax-1b": true,
	}

	got := filterToLocalZones(results, localZones)

	if len(got) != 2 {
		t.Fatalf("expected 2 results after filtering to Local Zones, got %d: %+v", len(got), got)
	}
	if got[0].InstanceType != "m5.large" {
		t.Fatalf("expected first kept result to be m5.large, got %s", got[0].InstanceType)
	}
	// The mixed result must be narrowed to only its edge AZ.
	if !reflect.DeepEqual(got[0].AvailableAZs, []string{"us-east-1-bos-1a"}) {
		t.Errorf("m5.large AZs = %v, want [us-east-1-bos-1a]", got[0].AvailableAZs)
	}
	if got[1].InstanceType != "g4dn.xlarge" {
		t.Fatalf("expected second kept result to be g4dn.xlarge, got %s", got[1].InstanceType)
	}
	if !reflect.DeepEqual(got[1].AvailableAZs, []string{"us-west-2-lax-1a", "us-west-2-lax-1b"}) {
		t.Errorf("g4dn.xlarge AZs = %v, want both lax AZs", got[1].AvailableAZs)
	}

	// The input slice must be untouched (filter copies rather than mutates): the
	// mixed result still carries all three of its original AZs.
	if len(results[0].AvailableAZs) != 3 {
		t.Errorf("input results[0].AvailableAZs was mutated: %v", results[0].AvailableAZs)
	}
}

// TestFilterToLocalZonesEmptySet: when nothing classified as an edge zone (nil
// set — e.g. no Local Zones in the searched regions, or classification failed),
// every result is dropped, which the callers surface as "no results".
func TestFilterToLocalZonesEmptySet(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "m5.large", Region: "us-east-1", AvailableAZs: []string{"us-east-1a", "us-east-1b"}},
	}
	if got := filterToLocalZones(results, nil); len(got) != 0 {
		t.Errorf("filterToLocalZones with nil set = %d results, want 0", len(got))
	}
	if got := filterToLocalZones(results, map[string]bool{}); len(got) != 0 {
		t.Errorf("filterToLocalZones with empty set = %d results, want 0", len(got))
	}
}

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// render runs the obtainability report and returns it as text.
func render(t *testing.T, o *aws.Obtainability) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printObtainability(&buf, o); err != nil {
		t.Fatalf("printObtainability: %v", err)
	}
	return buf.String()
}

func intPtr(n int) *int { return &n }

// TestPrintObtainability_LowObtainability covers the case the command exists for
// (#108): a scarce accelerator type where every signal is bad. Each number must
// arrive with the context that makes it actionable — the AZ denominator, the quota
// code to request against, and the window the offering count was measured over.
func TestPrintObtainability_LowObtainability(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType: "p6-b200.48xlarge",
		Region:       "us-east-1",
		SpotPlacements: []aws.SpotPlacement{
			{AZ: "us-east-1a", AZID: "use1-az2", Score: 1},
		},
		OfferedAZs:               []string{"us-east-1a"},
		TotalAZs:                 6,
		OnDemandQuotaHeadroom:    intPtr(0),
		QuotaFamily:              "P",
		QuotaCode:                "L-417A185B",
		VCPUsPerInstance:         192,
		CapacityBlockOfferings:   intPtr(0),
		CapacityBlockWindowHours: 24,
	})

	for _, want := range []string{
		"OBTAINABILITY", "p6-b200.48xlarge", "us-east-1",
		"1/10",                // the spot score
		"use1-az2",            // the AZ ID, stable across accounts
		"1 of 6",              // the offered-AZ count WITH its denominator
		"single-AZ footprint", // ... and why that matters
		"L-417A185B",          // the quota to raise
		"0 offerings",         // capacity blocks
		"24h window",          // ... over a stated window
		"low obtainability",   // the verdict
		"lagotto watch",       // the remedy
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintObtainability_NamesTheDrivingSignals verifies the verdict says which
// signals drove it. A bare "low obtainability" is an opaque score; naming the
// signals lets a reader check the reasoning and pick the right remedy, since a zero
// quota (file an increase) and a 1/10 spot score (wait, or switch to on-demand) are
// different problems.
func TestPrintObtainability_NamesTheDrivingSignals(t *testing.T) {
	// Only the quota is a problem here: good spot score, wide AZ footprint.
	out := render(t, &aws.Obtainability{
		InstanceType:             "p5.48xlarge",
		Region:                   "us-west-2",
		SpotPlacements:           []aws.SpotPlacement{{AZ: "us-west-2b", AZID: "usw2-az1", Score: 9}},
		OfferedAZs:               []string{"us-west-2a", "us-west-2b", "us-west-2c"},
		TotalAZs:                 4,
		OnDemandQuotaHeadroom:    intPtr(0),
		QuotaCode:                "L-417A185B",
		VCPUsPerInstance:         192,
		CapacityBlockOfferings:   intPtr(3),
		CapacityBlockWindowHours: 24,
	})

	if !strings.Contains(out, "quota") {
		t.Errorf("verdict does not name the quota signal that drove it:\n%s", out)
	}
	if strings.Contains(out, "low obtainability (spot") {
		t.Errorf("a 9/10 spot score was reported as a concern:\n%s", out)
	}
	if !strings.Contains(out, "3 offerings") {
		t.Errorf("capacity block offerings not shown:\n%s", out)
	}
}

// TestPrintObtainability_ClearResult verifies a type with no blocking signals says
// so plainly rather than emitting an empty report the reader has to interpret.
func TestPrintObtainability_ClearResult(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType:             "m5.large",
		Region:                   "us-east-1",
		SpotPlacements:           []aws.SpotPlacement{{AZ: "us-east-1a", AZID: "use1-az2", Score: 10}},
		OfferedAZs:               []string{"us-east-1a", "us-east-1b", "us-east-1c"},
		TotalAZs:                 6,
		OnDemandQuotaHeadroom:    intPtr(640),
		QuotaCode:                "L-1216C47A",
		VCPUsPerInstance:         2,
		CapacityBlockOfferings:   intPtr(0),
		CapacityBlockWindowHours: 24,
	})

	if !strings.Contains(out, "no blocking signals") {
		t.Errorf("clear result not stated plainly:\n%s", out)
	}
	if strings.Contains(out, "low obtainability") {
		t.Errorf("clear result reported as low obtainability:\n%s", out)
	}
	// 640 vCPU / 2 = 320 instances — headroom must convert to something usable.
	if !strings.Contains(out, "320 instances") {
		t.Errorf("vCPU headroom not converted to an instance count:\n%s", out)
	}
}

// TestPrintObtainability_MissingSignalsAreVisible is the anti-silence check: an
// unavailable signal must render as unknown AND appear in the warnings, never as a
// blank that reads like a good result. GetSpotPlacementScores is commonly denied by
// SCP, so this is the routine case, not an edge one.
func TestPrintObtainability_MissingSignalsAreVisible(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType: "p5.48xlarge",
		Region:       "us-east-1",
		OfferedAZs:   []string{"us-east-1a", "us-east-1b"},
		TotalAZs:     6,
		Warnings: []string{
			"spot placement scores unavailable: AccessDenied",
			"quota headroom unavailable: AccessDenied",
		},
	})

	if !strings.Contains(out, "no score available") {
		t.Errorf("absent spot score not marked as absent:\n%s", out)
	}
	if !strings.Contains(out, "AccessDenied") {
		t.Errorf("warnings not surfaced in the report:\n%s", out)
	}
	if strings.Contains(out, "0/10") {
		t.Errorf("an absent spot score was rendered as a zero score:\n%s", out)
	}
	// An absent quota must not print as "0 vCPU", which would read as a hard block.
	if strings.Contains(out, "0 vCPU") {
		t.Errorf("absent quota headroom rendered as 0 vCPU:\n%s", out)
	}
}

// TestPrintObtainability_NotOfferedInRegion covers the hard "wrong region" answer:
// zero offered AZs is not a scarcity signal, it means this type isn't in this
// region at all and no amount of waiting will change that.
func TestPrintObtainability_NotOfferedInRegion(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType: "p6-b200.48xlarge",
		Region:       "eu-west-3",
		OfferedAZs:   nil,
		TotalAZs:     3,
	})

	if !strings.Contains(out, "not offered in this region") {
		t.Errorf("zero offered AZs not called out:\n%s", out)
	}
	if !strings.Contains(out, "low obtainability") {
		t.Errorf("a type not offered in the region was not flagged:\n%s", out)
	}
}

// TestPrintObtainability_SingleAZRegionIsNotAWarning verifies "1 of 1" doesn't
// trigger the single-AZ warning. The warning is about concentration risk — being
// the only AZ in a one-AZ region is not a risk signal, and a false warning here
// would train readers to ignore the real ones.
func TestPrintObtainability_SingleAZRegionIsNotAWarning(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType:          "m5.large",
		Region:                "ap-southeast-5",
		OfferedAZs:            []string{"ap-southeast-5a"},
		TotalAZs:              1,
		OnDemandQuotaHeadroom: intPtr(64),
		VCPUsPerInstance:      2,
	})

	if strings.Contains(out, "single-AZ footprint") {
		t.Errorf("1-of-1 flagged as a single-AZ concentration risk:\n%s", out)
	}
	if !strings.Contains(out, "1 of 1") {
		t.Errorf("AZ ratio not shown:\n%s", out)
	}
}

// TestPrintObtainability_HeadroomTooSmallForOneInstance covers the trap where a
// non-zero quota still can't launch anything: 96 vCPU of headroom against a
// 192-vCPU instance is a hard block that a bare "96 vCPU" would hide.
func TestPrintObtainability_HeadroomTooSmallForOneInstance(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType:          "p5.48xlarge",
		Region:                "us-east-1",
		OfferedAZs:            []string{"us-east-1a", "us-east-1b"},
		TotalAZs:              6,
		OnDemandQuotaHeadroom: intPtr(96),
		QuotaCode:             "L-417A185B",
		VCPUsPerInstance:      192,
	})

	if !strings.Contains(out, "0 instances") {
		t.Errorf("headroom insufficient for one instance not shown as 0 instances:\n%s", out)
	}
	if !strings.Contains(out, "low obtainability") {
		t.Errorf("non-zero-but-unusable headroom was not treated as a concern:\n%s", out)
	}
}

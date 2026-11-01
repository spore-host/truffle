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

func floatPtr(f float64) *float64 { return &f }

// TestPrintObtainability_ShowsOnDemandPrice verifies the report carries the
// on-demand rate at discovery time (#159), labeled with its source so a live
// Price List rate and a possibly-stale fallback rate are distinguishable.
func TestPrintObtainability_ShowsOnDemandPrice(t *testing.T) {
	tests := []struct {
		name   string
		price  *float64
		source aws.PriceSource
		want   []string
		absent []string
	}{
		{
			name:   "live rate",
			price:  floatPtr(0.7838),
			source: aws.PriceSourceLive,
			want:   []string{"On-demand price", "$0.7838/hr", "on-demand (live)"},
		},
		{
			name:   "static fallback rate",
			price:  floatPtr(0.7838),
			source: aws.PriceSourceStatic,
			want:   []string{"On-demand price", "$0.7838/hr", "static fallback"},
			absent: []string{"(live)"},
		},
		{
			name:   "unavailable price is honest, not fabricated",
			price:  nil,
			source: aws.PriceSourceUnknown,
			want:   []string{"On-demand price", "unavailable"},
			absent: []string{"$0.0000/hr", "$0.00/hr"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := render(t, &aws.Obtainability{
				InstanceType:          "c8gd.4xlarge",
				Region:                "us-east-1",
				OfferedAZs:            []string{"us-east-1a", "us-east-1b"},
				TotalAZs:              6,
				OnDemandQuotaHeadroom: intPtr(2040),
				VCPUsPerInstance:      16,
				OnDemandPrice:         tc.price,
				OnDemandPriceSource:   tc.source,
			})
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Errorf("output should not contain %q:\n%s", absent, out)
				}
			}
		})
	}
}

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

// TestPrintObtainability_VerdictNeverClaimsOnDemandCapacity is the #272 gate. No
// signal truffle collects observes On-Demand capacity (AWS has no API for it), so
// the clear verdict must say which signals it checked, say On-Demand capacity
// isn't one of them, and point at lagotto for waiting. Before #272 it printed a
// bare "no blocking signals found", read as "On-Demand will work" while On-Demand
// launches were failing in every AZ.
func TestPrintObtainability_VerdictNeverClaimsOnDemandCapacity(t *testing.T) {
	tests := []struct {
		name        string
		o           *aws.Obtainability
		wantChecked string
	}{
		{
			name: "all signals present",
			o: &aws.Obtainability{
				InstanceType:          "m5.large",
				Region:                "us-east-1",
				SpotPlacements:        []aws.SpotPlacement{{AZ: "us-east-1a", AZID: "use1-az2", Score: 10}},
				OfferedAZs:            []string{"us-east-1a", "us-east-1b", "us-east-1c"},
				TotalAZs:              6,
				OnDemandQuotaHeadroom: intPtr(640),
				VCPUsPerInstance:      2,
			},
			wantChecked: "no blocking signals among: Spot placement, offered AZs, quota",
		},
		{
			// A signal that wasn't collected must not be named as checked.
			name: "spot and quota unavailable",
			o: &aws.Obtainability{
				InstanceType: "p5.48xlarge",
				Region:       "us-east-1",
				OfferedAZs:   []string{"us-east-1a", "us-east-1b"},
				TotalAZs:     6,
				Warnings:     []string{"spot placement scores unavailable: AccessDenied"},
			},
			wantChecked: "no blocking signals among: offered AZs\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := render(t, tt.o)
			if !strings.Contains(out, tt.wantChecked) {
				t.Errorf("verdict does not name exactly the checked signals (want %q):\n%s", tt.wantChecked, out)
			}
			if !strings.Contains(out, "on-demand capacity can't be checked") {
				t.Errorf("clear verdict does not say on-demand capacity is unchecked:\n%s", out)
			}
			if want := "`lagotto watch " + tt.o.InstanceType + "`"; !strings.Contains(out, want) {
				t.Errorf("clear verdict does not suggest %s for waiting:\n%s", want, out)
			}
			if strings.Contains(out, "no blocking signals found") {
				t.Errorf("unqualified clear verdict is back:\n%s", out)
			}
		})
	}
}

// TestPrintObtainability_CapacityBlockIneligible pins the #272 rendering: a type
// AWS doesn't sell as a Capacity Block gets a plain "not eligible" row, not
// "(unavailable)" and not a raw API error.
func TestPrintObtainability_CapacityBlockIneligible(t *testing.T) {
	out := render(t, &aws.Obtainability{
		InstanceType:       "c7i.4xlarge",
		Region:             "us-west-2",
		OfferedAZs:         []string{"us-west-2a"},
		TotalAZs:           4,
		CapacityBlockState: aws.CapacityBlockIneligible,
	})
	if !strings.Contains(out, "not eligible for c7i.4xlarge in us-west-2") {
		t.Errorf("ineligible capacity blocks not rendered as such:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Capacity blocks") && strings.Contains(line, "unavailable") {
			t.Errorf("ineligible rendered as unavailable: %q", line)
		}
	}
}

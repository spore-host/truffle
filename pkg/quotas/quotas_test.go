package quotas

import (
	"strings"
	"testing"
)

func TestGetQuotaFamily(t *testing.T) {
	tests := []struct {
		instanceType string
		want         QuotaFamily
	}{
		{"p5.48xlarge", FamilyP},
		{"p3.2xlarge", FamilyP},
		{"g4dn.xlarge", FamilyG},
		{"g5.2xlarge", FamilyG},
		{"inf1.xlarge", FamilyInf},
		{"inf2.8xlarge", FamilyInf},
		{"trn1.2xlarge", FamilyTrn},
		{"trn1n.32xlarge", FamilyTrn},
		{"f1.2xlarge", FamilyF},
		{"x2gd.xlarge", FamilyX},
		{"x1e.32xlarge", FamilyX},
		// DL accelerators (#64): dl1 (Habana Gaudi), dl2q (Qualcomm) → their own family.
		{"dl1.24xlarge", FamilyDL},
		{"dl2q.24xlarge", FamilyDL},
		// VT (video transcoding) shares the G-and-VT quota → FamilyG (#64).
		{"vt1.3xlarge", FamilyG},
		{"vt1.24xlarge", FamilyG},
		{"m7g.large", FamilyStandard},
		{"c6i.xlarge", FamilyStandard},
		{"r6a.2xlarge", FamilyStandard},
		{"t4g.medium", FamilyStandard},
		{"", FamilyStandard},
	}

	for _, tt := range tests {
		t.Run(tt.instanceType, func(t *testing.T) {
			got := GetQuotaFamily(tt.instanceType)
			if got != tt.want {
				t.Errorf("GetQuotaFamily(%q) = %v, want %v", tt.instanceType, got, tt.want)
			}
		})
	}
}

func TestGetVCPUCount(t *testing.T) {
	tests := []struct {
		instanceType string
		want         int32
	}{
		{"t4g.nano", 1},
		{"t4g.micro", 1},
		{"t4g.small", 1},
		{"t4g.medium", 1},
		{"t4g.large", 2},
		{"m7g.xlarge", 4},
		{"c6i.2xlarge", 8},
		{"r6a.4xlarge", 16},
		{"m6i.8xlarge", 32},
		{"c6i.16xlarge", 64},
		{"p5.48xlarge", 192},
		{"unknown.size", 2},
		{"nosize", 2},
	}

	for _, tt := range tests {
		t.Run(tt.instanceType, func(t *testing.T) {
			got := getVCPUCount(tt.instanceType)
			if got != tt.want {
				t.Errorf("getVCPUCount(%q) = %d, want %d", tt.instanceType, got, tt.want)
			}
		})
	}
}

func makeQuotaInfo(onDemand, spot, usage map[QuotaFamily]int32) *QuotaInfo {
	return &QuotaInfo{
		Region:   "us-east-1",
		OnDemand: onDemand,
		Spot:     spot,
		Usage:    usage,
	}
}

func makeQuotaInfoWithSpotUsage(onDemand, spot, usage, spotUsage map[QuotaFamily]int32) *QuotaInfo {
	info := makeQuotaInfo(onDemand, spot, usage)
	info.SpotUsage = spotUsage
	return info
}

func TestCanLaunch(t *testing.T) {
	c := &Client{}

	quotas := makeQuotaInfo(
		map[QuotaFamily]int32{FamilyStandard: 32, FamilyG: 8},
		map[QuotaFamily]int32{FamilyStandard: 64, FamilyG: 0},
		map[QuotaFamily]int32{FamilyStandard: 16, FamilyG: 0},
	)

	tests := []struct {
		name         string
		instanceType string
		vCPUs        int32
		spot         bool
		wantOK       bool
		wantMsgPart  string
	}{
		{
			name:         "on-demand fits",
			instanceType: "m7g.xlarge",
			vCPUs:        4,
			spot:         false,
			wantOK:       true,
		},
		{
			name:         "on-demand exceeds available",
			instanceType: "m7g.xlarge",
			vCPUs:        20, // only 16 available (32-16)
			spot:         false,
			wantOK:       false,
			wantMsgPart:  "Need 20 vCPUs",
		},
		{
			name:         "spot quota zero",
			instanceType: "g5.xlarge",
			vCPUs:        4,
			spot:         true,
			wantOK:       false,
			wantMsgPart:  "quota for G instances is 0",
		},
		{
			name:         "spot fits",
			instanceType: "m7g.xlarge",
			vCPUs:        4,
			spot:         true,
			wantOK:       true,
		},
		{
			name:         "on-demand quota zero",
			instanceType: "m7g.xlarge",
			vCPUs:        4,
			spot:         false,
			wantOK:       false,
			wantMsgPart:  "quota",
		},
	}

	// Override standard on-demand quota to 0 for last test
	tests[4].name = "on-demand quota zero"
	zeroQuotas := makeQuotaInfo(
		map[QuotaFamily]int32{FamilyStandard: 0},
		map[QuotaFamily]int32{FamilyStandard: 64},
		map[QuotaFamily]int32{FamilyStandard: 0},
	)

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := quotas
			if i == 4 {
				q = zeroQuotas
			}
			ok, msg := c.CanLaunch(tt.instanceType, tt.vCPUs, q, tt.spot)
			if ok != tt.wantOK {
				t.Errorf("CanLaunch() ok = %v, want %v (msg: %s)", ok, tt.wantOK, msg)
			}
			if tt.wantMsgPart != "" && !strings.Contains(msg, tt.wantMsgPart) {
				t.Errorf("CanLaunch() msg = %q, want substring %q", msg, tt.wantMsgPart)
			}
		})
	}
}

// TestCanLaunch_SpotTracksCurrentUsage is the #132 regression guard: the
// real-world calque incident had a 64-vCPU G/VT Spot quota already fully
// saturated by 8 running g7e.2xlarge (8 vCPUs each = 64), then asked for 2
// more shards (16 more vCPUs). Before #132, CanLaunch reported "fits" for
// each of those 2 requests in isolation (16 <= 64, the FULL quota) with no
// signal the quota was already saturated by the caller's own instances — the
// actual RunInstances call then failed with MaxSpotInstanceCountExceeded.
func TestCanLaunch_SpotTracksCurrentUsage(t *testing.T) {
	c := &Client{}

	tests := []struct {
		name        string
		spotUsage   map[QuotaFamily]int32
		vCPUs       int32
		wantOK      bool
		wantMsgPart string
	}{
		{
			name:        "quota fully saturated by existing spot usage",
			spotUsage:   map[QuotaFamily]int32{FamilyG: 64}, // 8x g7e.2xlarge already running
			vCPUs:       16,                                 // 2 more shards
			wantOK:      false,
			wantMsgPart: "only 0 available",
		},
		{
			name:      "quota partially used, request fits remaining headroom",
			spotUsage: map[QuotaFamily]int32{FamilyG: 32},
			vCPUs:     16,
			wantOK:    true,
		},
		{
			name:        "quota partially used, request exceeds remaining headroom",
			spotUsage:   map[QuotaFamily]int32{FamilyG: 56},
			vCPUs:       16,
			wantOK:      false,
			wantMsgPart: "only 8 available",
		},
		{
			name:      "no SpotUsage tracked (nil map) behaves as before — full quota available",
			spotUsage: nil,
			vCPUs:     64,
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quotas := makeQuotaInfoWithSpotUsage(
				nil,
				map[QuotaFamily]int32{FamilyG: 64},
				nil,
				tt.spotUsage,
			)
			ok, msg := c.CanLaunch("g7e.2xlarge", tt.vCPUs, quotas, true)
			if ok != tt.wantOK {
				t.Errorf("CanLaunch() ok = %v, want %v (msg: %s)", ok, tt.wantOK, msg)
			}
			if tt.wantMsgPart != "" && !strings.Contains(msg, tt.wantMsgPart) {
				t.Errorf("CanLaunch() msg = %q, want substring %q", msg, tt.wantMsgPart)
			}
		})
	}
}

func TestQuotaIncreaseCommand(t *testing.T) {
	cmd := QuotaIncreaseCommand("us-east-1", FamilyStandard, 256, false)
	if !strings.Contains(cmd, "us-east-1") {
		t.Errorf("command missing region: %s", cmd)
	}
	if !strings.Contains(cmd, "256") {
		t.Errorf("command missing desired value: %s", cmd)
	}

	spotCmd := QuotaIncreaseCommand("eu-west-1", FamilyG, 64, true)
	if !strings.Contains(spotCmd, "eu-west-1") {
		t.Errorf("spot command missing region: %s", spotCmd)
	}
	if !strings.Contains(spotCmd, "64") {
		t.Errorf("spot command missing desired value: %s", spotCmd)
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int32
	}{
		{"4", 4},
		{"48", 48},
		{"0", 0},
		{"", 0},
		{"abc", 0},
	}
	for _, tt := range tests {
		got := parseInt(tt.input)
		if got != tt.want {
			t.Errorf("parseInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

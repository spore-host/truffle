package quotas

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

// allQuotaFamilies is every QuotaFamily the package defines. The length guard in
// TestQuotaIncreaseCommand_EveryFamilyAndLifecycle fails if a family is added to
// the code tables without being added here, so the exhaustive sweep below can't
// quietly stop being exhaustive.
var allQuotaFamilies = []QuotaFamily{
	FamilyStandard, FamilyF, FamilyG, FamilyP, FamilyX, FamilyInf, FamilyTrn, FamilyDL,
}

// wantQuotaCodes is the expected (family, lifecycle) → code mapping, written out
// independently of the lookup under test.
var wantQuotaCodes = map[quotaKey]string{
	{FamilyStandard, false}: QuotaCodeStandard,
	{FamilyF, false}:        QuotaCodeF,
	{FamilyG, false}:        QuotaCodeG,
	{FamilyP, false}:        QuotaCodeP,
	{FamilyX, false}:        QuotaCodeX,
	{FamilyInf, false}:      QuotaCodeInf,
	{FamilyTrn, false}:      QuotaCodeTrn,
	{FamilyDL, false}:       QuotaCodeDL,
	{FamilyStandard, true}:  QuotaCodeSpotStandard,
	{FamilyF, true}:         QuotaCodeSpotF,
	{FamilyG, true}:         QuotaCodeSpotG,
	{FamilyP, true}:         QuotaCodeSpotP,
	{FamilyX, true}:         QuotaCodeSpotX,
	{FamilyInf, true}:       QuotaCodeSpotInf,
	{FamilyTrn, true}:       QuotaCodeSpotTrn,
	{FamilyDL, true}:        QuotaCodeSpotDL,
}

type quotaKey struct {
	family QuotaFamily
	spot   bool
}

// TestQuotaIncreaseCommand_EveryFamilyAndLifecycle is the #167 regression guard.
// The bug was a partial switch with a `default:` that silently emitted the
// Standard On-Demand code L-1216C47A: X/DL/F on-demand and every non-G/P spot
// family got a confident, copy-pasteable command that raised an UNRELATED quota
// (and, on the spot path, the wrong lifecycle). So this sweeps all 8 families x
// both lifecycles and asserts each emits its OWN code — and, crucially, that no
// pair emits L-1216C47A unless it genuinely is Standard on-demand.
func TestQuotaIncreaseCommand_EveryFamilyAndLifecycle(t *testing.T) {
	if len(onDemandQuotaCodes) != len(allQuotaFamilies) || len(spotQuotaCodes) != len(allQuotaFamilies) {
		t.Fatalf("code tables cover %d on-demand / %d spot families but allQuotaFamilies lists %d — add the new family to allQuotaFamilies and wantQuotaCodes",
			len(onDemandQuotaCodes), len(spotQuotaCodes), len(allQuotaFamilies))
	}

	for _, family := range allQuotaFamilies {
		for _, spot := range []bool{false, true} {
			name := string(family) + "/" + lifecycleLabel(spot)
			t.Run(name, func(t *testing.T) {
				wantCode, ok := wantQuotaCodes[quotaKey{family, spot}]
				if !ok {
					t.Fatalf("no expectation recorded for %s", name)
				}

				gotCode, gotOK := QuotaCodeFor(family, spot)
				if !gotOK {
					t.Fatalf("QuotaCodeFor(%s, %v) reported unmapped, want %s", family, spot, wantCode)
				}
				if gotCode != wantCode {
					t.Errorf("QuotaCodeFor(%s, %v) = %s, want %s", family, spot, gotCode, wantCode)
				}

				cmd := QuotaIncreaseCommand("us-east-1", family, 64, spot)
				if !strings.Contains(cmd, "--quota-code "+wantCode) {
					t.Errorf("command for %s does not request %s:\n%s", name, wantCode, cmd)
				}
				// The wrong-code trap: only Standard on-demand may mention
				// L-1216C47A.
				isStandardOnDemand := family == FamilyStandard && !spot
				if !isStandardOnDemand && strings.Contains(cmd, QuotaCodeStandard) {
					t.Errorf("command for %s leaked the Standard On-Demand code %s (#167):\n%s", name, QuotaCodeStandard, cmd)
				}
				// A spot shortfall must not be answered with an On-Demand label.
				if spot && strings.Contains(cmd, "On-Demand") {
					t.Errorf("spot command for %s is labelled On-Demand (#167):\n%s", name, cmd)
				}
				if !strings.Contains(cmd, "request-service-quota-increase") {
					t.Errorf("command for %s is not a quota-increase request:\n%s", name, cmd)
				}
			})
		}
	}
}

// TestQuotaIncreaseCommand_UnmappedFamily covers the explicit fallthrough: a
// family with no code must yield a console pointer, never a command built from
// some other family's code.
func TestQuotaIncreaseCommand_UnmappedFamily(t *testing.T) {
	// A family value the package has no code for (e.g. a hypothetical future
	// accelerator someone forgot to add to the code tables).
	unknown := QuotaFamily("Quantum")

	for _, spot := range []bool{false, true} {
		if code, ok := QuotaCodeFor(unknown, spot); ok {
			t.Errorf("QuotaCodeFor(%s, %v) = %s, want not-mapped", unknown, spot, code)
		}

		cmd := QuotaIncreaseCommand("us-east-1", unknown, 64, spot)
		if strings.Contains(cmd, "request-service-quota-increase") {
			t.Errorf("unmapped family (spot=%v) emitted a runnable command:\n%s", spot, cmd)
		}
		if strings.Contains(cmd, QuotaCodeStandard) {
			t.Errorf("unmapped family (spot=%v) fell back to the Standard On-Demand code (#167):\n%s", spot, cmd)
		}
		if !strings.Contains(cmd, "servicequotas") {
			t.Errorf("unmapped family (spot=%v) does not point at the Service Quotas console:\n%s", spot, cmd)
		}
	}
}

func TestVCPUsForType(t *testing.T) {
	tests := []struct {
		in     string
		want   int32
		wantOK bool
	}{
		{"t4g.nano", 1, true},
		{"m8g.medium", 1, true},
		{"g6e.large", 2, true},
		{"g6e.xlarge", 4, true},
		{"c6i.2xlarge", 8, true},
		{"r6a.4xlarge", 16, true},
		{"p5.48xlarge", 192, true},
		{"u7in.112xlarge", 448, true},
		// The NxLarge = N*4 general pattern (a size the table doesn't list).
		{"x9.20xlarge", 80, true},
		// Not countable: wildcards/regex, family-only, empty, unknown size.
		{"g6e.*", 0, false},
		{"g6e", 0, false},
		{"", 0, false},
		{"g6e.", 0, false},
		{".xlarge", 0, false},
		{"g6e.humongous", 0, false},
		{`^g6e\.xlarge$`, 0, false},
		{"g6e.xlarge.extra", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := VCPUsForType(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("VCPUsForType(%q) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestCanLaunch_AbsentFamilyIsNotZero is the other half of #167: a family whose
// quota LOOKUP FAILED is absent from the snapshot, and telling that user to
// "request a quota increase" is wrong advice when the real cause is a missing
// servicequotas:GetServiceQuota permission.
func TestCanLaunch_AbsentFamilyIsNotZero(t *testing.T) {
	c := &Client{}

	t.Run("absent family reports undetermined, not zero", func(t *testing.T) {
		info := makeQuotaInfo(
			map[QuotaFamily]int32{FamilyStandard: 32}, // no FamilyG key at all
			nil, nil,
		)
		ok, msg := c.CanLaunch("g5.xlarge", 4, info, false)
		if ok {
			t.Fatalf("CanLaunch() ok = true, want false (msg: %s)", msg)
		}
		if strings.Contains(msg, "is 0") {
			t.Errorf("absent family reported as zero quota: %q", msg)
		}
		if !strings.Contains(msg, "could not determine") {
			t.Errorf("msg = %q, want it to say the quota could not be determined", msg)
		}
	})

	t.Run("recorded lookup error is surfaced", func(t *testing.T) {
		info := makeQuotaInfo(map[QuotaFamily]int32{FamilyStandard: 32}, nil, nil)
		info.OnDemandErrors = map[QuotaFamily]error{
			FamilyG: errors.New("AccessDeniedException: servicequotas:GetServiceQuota"),
		}
		ok, msg := c.CanLaunch("g5.xlarge", 4, info, false)
		if ok {
			t.Fatalf("CanLaunch() ok = true, want false (msg: %s)", msg)
		}
		if !strings.Contains(msg, "AccessDeniedException") {
			t.Errorf("msg = %q, want the recorded lookup error", msg)
		}
		if strings.Contains(msg, "is 0") {
			t.Errorf("permission error reported as zero quota: %q", msg)
		}
	})

	t.Run("genuinely zero still advises a quota increase", func(t *testing.T) {
		info := makeQuotaInfo(map[QuotaFamily]int32{FamilyG: 0}, nil, nil)
		ok, msg := c.CanLaunch("g5.xlarge", 4, info, false)
		if ok {
			t.Fatalf("CanLaunch() ok = true, want false (msg: %s)", msg)
		}
		if !strings.Contains(msg, "is 0") || !strings.Contains(msg, "request quota increase") {
			t.Errorf("msg = %q, want the zero-quota advice", msg)
		}
	})

	t.Run("absent spot family reports undetermined", func(t *testing.T) {
		info := makeQuotaInfo(nil, map[QuotaFamily]int32{FamilyStandard: 64}, nil)
		ok, msg := c.CanLaunch("g5.xlarge", 4, info, true)
		if ok {
			t.Fatalf("CanLaunch() ok = true, want false (msg: %s)", msg)
		}
		if strings.Contains(msg, "is 0") {
			t.Errorf("absent spot family reported as zero quota: %q", msg)
		}
		if !strings.Contains(msg, "Spot") {
			t.Errorf("msg = %q, want it to name the Spot lifecycle", msg)
		}
	})
}

// TestGetQuotas_RecordsPerFamilyLookupFailures drives GetQuotas through its test
// seams (no AWS): a failing per-family lookup must be RECORDED and leave the
// family key absent, while the successful ones are populated. Before #167 the
// error was dropped on the floor, which is what made "0" ambiguous.
func TestGetQuotas_RecordsPerFamilyLookupFailures(t *testing.T) {
	denied := errors.New("AccessDeniedException: not authorized to perform servicequotas:GetServiceQuota")

	c := &Client{
		cache:    make(map[string]*QuotaInfo),
		cacheTTL: time.Minute,
		quotaValueFn: func(_ context.Context, _, quotaCode string) (int32, error) {
			switch quotaCode {
			case QuotaCodeG, QuotaCodeSpotX:
				return 0, denied
			case QuotaCodeStandard:
				return 32, nil
			case QuotaCodeP:
				return 0, nil // genuinely zero, not a failure
			}
			return 8, nil
		},
		usageFn: func(_ context.Context, _ string) (map[QuotaFamily]int32, map[QuotaFamily]int32, error) {
			return map[QuotaFamily]int32{FamilyStandard: 4}, map[QuotaFamily]int32{}, nil
		},
		runningCountFn: func(_ context.Context, _ string) (int32, error) { return 1, nil },
	}

	info, err := c.GetQuotas(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("GetQuotas() error = %v", err)
	}

	// Failed on-demand lookup: key absent, error recorded.
	if v, ok := info.OnDemand[FamilyG]; ok {
		t.Errorf("OnDemand[FamilyG] = %d, want absent after a failed lookup", v)
	}
	if got := info.LookupError(FamilyG, false); !errors.Is(got, denied) {
		t.Errorf("LookupError(FamilyG, false) = %v, want the lookup failure", got)
	}
	if got := info.MissingFamilies(false); len(got) != 1 || got[0] != FamilyG {
		t.Errorf("MissingFamilies(false) = %v, want [G]", got)
	}

	// Failed spot lookup, independently tracked.
	if _, ok := info.Spot[FamilyX]; ok {
		t.Error("Spot[FamilyX] present, want absent after a failed lookup")
	}
	if got := info.LookupError(FamilyX, true); !errors.Is(got, denied) {
		t.Errorf("LookupError(FamilyX, true) = %v, want the lookup failure", got)
	}
	if got := info.MissingFamilies(true); len(got) != 1 || got[0] != FamilyX {
		t.Errorf("MissingFamilies(true) = %v, want [X]", got)
	}
	// The same family's on-demand lookup succeeded — the two lifecycles don't
	// contaminate each other.
	if got := info.LookupError(FamilyX, false); got != nil {
		t.Errorf("LookupError(FamilyX, false) = %v, want nil", got)
	}

	// Successes populate values, including a genuine zero (present-with-0).
	if v, ok := info.OnDemand[FamilyStandard]; !ok || v != 32 {
		t.Errorf("OnDemand[FamilyStandard] = (%d, %v), want (32, true)", v, ok)
	}
	v, ok := info.OnDemand[FamilyP]
	if !ok || v != 0 {
		t.Errorf("OnDemand[FamilyP] = (%d, %v), want (0, true) — a genuine zero stays PRESENT", v, ok)
	}
	if info.LookupError(FamilyP, false) != nil {
		t.Errorf("LookupError(FamilyP, false) = %v, want nil for a genuine zero", info.LookupError(FamilyP, false))
	}

	// CanLaunch must distinguish the two: undetermined for G, zero for P.
	if _, msg := c.CanLaunch("g5.xlarge", 4, info, false); !strings.Contains(msg, "could not determine") {
		t.Errorf("CanLaunch on a failed lookup said %q, want an undetermined-quota message", msg)
	}
	if _, msg := c.CanLaunch("p5.48xlarge", 192, info, false); !strings.Contains(msg, "is 0") {
		t.Errorf("CanLaunch on a genuine zero said %q, want the zero-quota message", msg)
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

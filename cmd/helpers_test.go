package cmd

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
)

func TestWildcardToRegex(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		{"m5.large", []string{"m5.large"}, []string{"m5.xlarge", "m5large", "xm5.large"}},
		{"c7*", []string{"c7i.large", "c7a.xlarge", "c7"}, []string{"c6i.large", "ac7"}},
		{"m?.large", []string{"m5.large", "m7.large"}, []string{"m5x.large", "m.large"}},
		{"g5.*", []string{"g5.xlarge", "g5."}, []string{"g6.xlarge"}},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			re, err := regexp.Compile(wildcardToRegex(tt.pattern))
			if err != nil {
				t.Fatalf("wildcardToRegex(%q) produced invalid regex: %v", tt.pattern, err)
			}
			for _, m := range tt.match {
				if !re.MatchString(m) {
					t.Errorf("pattern %q should match %q (regex %q)", tt.pattern, m, re.String())
				}
			}
			for _, nm := range tt.noMatch {
				if re.MatchString(nm) {
					t.Errorf("pattern %q should NOT match %q (regex %q)", tt.pattern, nm, re.String())
				}
			}
		})
	}
}

func TestExtractFamily(t *testing.T) {
	tests := []struct{ in, want string }{
		{"p4d.24xlarge", "p4d"},
		{"m5.large", "m5"},
		{"nodot", "nodot"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := extractFamily(tt.in); got != tt.want {
			t.Errorf("extractFamily(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractFamilies(t *testing.T) {
	got := extractFamilies([]string{"m5.large", "m5.xlarge", "c6i.large", "t3.micro"})
	want := []string{"c6i", "m5", "t3"} // deduped + sorted
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractFamilies = %v, want %v", got, want)
	}
	if got := extractFamilies(nil); len(got) != 0 {
		t.Errorf("extractFamilies(nil) = %v, want empty", got)
	}
}

func TestExtractSizes(t *testing.T) {
	got := extractSizes([]string{"m5.large", "c6i.large", "t3.micro", "bare"})
	want := []string{"large", "micro"} // deduped + sorted; "bare" has no size
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractSizes = %v, want %v", got, want)
	}
}

func TestExtractRegionsFromAZs(t *testing.T) {
	got := extractRegionsFromAZs([]string{"us-east-1a", "us-east-1b", "us-west-2c"})
	// deduped, order not guaranteed — compare as sets
	set := map[string]bool{}
	for _, r := range got {
		set[r] = true
	}
	if !set["us-east-1"] || !set["us-west-2"] || len(set) != 2 {
		t.Errorf("extractRegionsFromAZs = %v, want {us-east-1, us-west-2}", got)
	}
	if got := extractRegionsFromAZs(nil); len(got) != 0 {
		t.Errorf("extractRegionsFromAZs(nil) = %v, want empty", got)
	}
}

func TestFilterByAZs(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "m5.large", AvailableAZs: []string{"us-east-1a", "us-east-1b", "us-east-1c"}},
		{InstanceType: "c6i.large", AvailableAZs: []string{"us-east-1d", "us-east-1e"}},
	}
	got := filterByAZs(results, []string{"us-east-1a", "us-east-1b"})
	if len(got) != 1 {
		t.Fatalf("expected 1 result matching the AZ filter, got %d", len(got))
	}
	if got[0].InstanceType != "m5.large" {
		t.Errorf("expected m5.large, got %s", got[0].InstanceType)
	}
	// Result's AZ list should be narrowed to only the matched AZs.
	if !reflect.DeepEqual(got[0].AvailableAZs, []string{"us-east-1a", "us-east-1b"}) {
		t.Errorf("matched AZs = %v, want [us-east-1a us-east-1b]", got[0].AvailableAZs)
	}
}

func TestFilterByMinAZCount(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "m5.large", AvailableAZs: []string{"a", "b", "c"}},
		{InstanceType: "c6i.large", AvailableAZs: []string{"a"}},
	}
	got := filterByMinAZCount(results, 2)
	if len(got) != 1 || got[0].InstanceType != "m5.large" {
		t.Errorf("filterByMinAZCount(2) = %v, want only m5.large", got)
	}
	if got := filterByMinAZCount(results, 0); len(got) != 2 {
		t.Errorf("filterByMinAZCount(0) should keep all, got %d", len(got))
	}
}

func TestFilterGPUInstances(t *testing.T) {
	results := []aws.CapacityReservationResult{
		{InstanceType: "p3.2xlarge"},    // V100
		{InstanceType: "g5.xlarge"},     // A10G
		{InstanceType: "m5.large"},      // not GPU
		{InstanceType: "trn1.32xlarge"}, // Trainium
		{InstanceType: "c6i.large"},     // not GPU
	}
	got := filterGPUInstances(results)
	if len(got) != 3 {
		t.Fatalf("expected 3 GPU/ML instances, got %d: %v", len(got), got)
	}
	families := map[string]bool{}
	for _, r := range got {
		families[extractFamily(r.InstanceType)] = true
	}
	for _, want := range []string{"p3", "g5", "trn1"} {
		if !families[want] {
			t.Errorf("expected GPU family %q in results", want)
		}
	}
}

// TestFilterGPUInstances_SuffixedVariants verifies the prefix matcher catches
// suffixed GPU families that the old exact-match map missed (regression #8).
func TestFilterGPUInstances_SuffixedVariants(t *testing.T) {
	gpu := []string{
		"p4d.24xlarge",   // A100 — the original #8 miss
		"p4de.24xlarge",  // A100 80GB
		"p5.48xlarge",    // H100
		"p5e.48xlarge",   // H200
		"p5en.48xlarge",  // H200 + EFAv3
		"g4dn.xlarge",    // T4
		"g4ad.xlarge",    // AMD Radeon
		"g6e.xlarge",     // L40S
		"trn1n.32xlarge", // Trainium + EFA
		"trn2.48xlarge",  // Trainium2
	}
	for _, it := range gpu {
		got := filterGPUInstances([]aws.CapacityReservationResult{{InstanceType: it}})
		if len(got) != 1 {
			t.Errorf("%s should be detected as GPU/ML, got %d matches", it, len(got))
		}
	}

	notGPU := []string{"m5.large", "c6i.large", "r7g.xlarge", "t3.micro", "i4i.large"}
	for _, it := range notGPU {
		got := filterGPUInstances([]aws.CapacityReservationResult{{InstanceType: it}})
		if len(got) != 0 {
			t.Errorf("%s should NOT be detected as GPU, got %d matches", it, len(got))
		}
	}
}

func TestIsGPUFamily(t *testing.T) {
	for _, f := range []string{"p3", "p4d", "p5en", "g4dn", "g5", "g6e", "inf2", "trn1n", "trn2", "vt1"} {
		if !isGPUFamily(f) {
			t.Errorf("isGPUFamily(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"m5", "c6i", "r7g", "t3", "i4i", "x2gd"} {
		if isGPUFamily(f) {
			t.Errorf("isGPUFamily(%q) = true, want false", f)
		}
	}
}

func TestGetQuotaStatus(t *testing.T) {
	tests := []struct {
		quota, usage int32
		want         string
	}{
		{0, 0, "zero"},        // no quota
		{100, 0, "healthy"},   // 100% available
		{100, 50, "healthy"},  // 50% available
		{100, 70, "warning"},  // 30% available
		{100, 80, "critical"}, // 20% available
		{100, 100, "zero"},    // 0% available
	}
	for _, tt := range tests {
		if got := getQuotaStatus(tt.quota, tt.usage); got != tt.want {
			t.Errorf("getQuotaStatus(%d, %d) = %q, want %q", tt.quota, tt.usage, got, tt.want)
		}
	}
}

func TestConvertToInstanceTypeResults(t *testing.T) {
	fr := []find.FindResult{
		{InstanceTypeResult: aws.InstanceTypeResult{InstanceType: "m5.large", Region: "us-east-1"}},
		{InstanceTypeResult: aws.InstanceTypeResult{InstanceType: "c6i.large", Region: "us-west-2"}},
	}
	got := convertToInstanceTypeResults(fr)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].InstanceType != "m5.large" || got[1].Region != "us-west-2" {
		t.Errorf("conversion lost data: %+v", got)
	}
	if got := convertToInstanceTypeResults(nil); len(got) != 0 {
		t.Errorf("convertToInstanceTypeResults(nil) = %v, want empty", got)
	}
}

// --- shell completion functions ---

func TestCompleteInstanceType(t *testing.T) {
	out, directive := completeInstanceType(nil, nil, "m7i")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(out) == 0 {
		t.Fatal("expected at least one m7i completion")
	}
	for _, c := range out {
		if !strings.HasPrefix(c, "m7i") {
			t.Errorf("completion %q does not start with m7i", c)
		}
	}
	// Empty prefix returns the full list.
	all, _ := completeInstanceType(nil, nil, "")
	if len(all) <= len(out) {
		t.Errorf("empty-prefix list (%d) should be larger than m7i list (%d)", len(all), len(out))
	}
}

func TestCompleteArchitecture(t *testing.T) {
	out, directive := completeArchitecture(nil, nil, "arm")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	if len(out) != 1 || !strings.HasPrefix(out[0], "arm64") {
		t.Errorf("arch completion for 'arm' = %v, want [arm64...]", out)
	}
	all, _ := completeArchitecture(nil, nil, "")
	if len(all) != 3 {
		t.Errorf("expected 3 architectures with empty prefix, got %d", len(all))
	}
}

func TestCompleteInstanceFamily(t *testing.T) {
	out, _ := completeInstanceFamily(nil, nil, "g")
	if len(out) == 0 {
		t.Fatal("expected GPU family completions for 'g'")
	}
	for _, c := range out {
		if !strings.HasPrefix(c, "g") {
			t.Errorf("family completion %q does not start with 'g'", c)
		}
	}
}

func TestCompleteRegion(t *testing.T) {
	out, directive := completeRegion(nil, nil, "us-")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	for _, c := range out {
		if !strings.HasPrefix(c, "us-") {
			t.Errorf("region completion %q does not start with us-", c)
		}
	}
}

func TestPatternToRegex(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		// Glob patterns (no regex metacharacters)
		{"m7i*", []string{"m7i.large", "m7i.xlarge"}, []string{"c7i.large"}},
		{"m7i.large", []string{"m7i.large"}, []string{"m7i.xlarge"}},
		{"c7.*", []string{"c7i.large", "c7g.xlarge", "c7a.metal"}, []string{"c6i.large"}},
		// Regex patterns (detected by brackets, +, etc.)
		{"c[6-8]i\\.large", []string{"c6i.large", "c7i.large", "c8i.large"}, []string{"c5i.large", "c6i.xlarge"}},
		{"m7[ig]\\..*", []string{"m7i.large", "m7g.xlarge"}, []string{"m7a.large"}},
		{"(p4d|p5)\\..*", []string{"p4d.24xlarge", "p5.48xlarge"}, []string{"p3.2xlarge"}},
		// Mixed glob/regex: bare * treated as .* inside regex-detected patterns
		{"c[6-8]*.xlarge", []string{"c6i.xlarge", "c7g.xlarge", "c8a.xlarge"}, []string{"c5i.xlarge", "c6i.large"}},
		{"c[6-8]i.large", []string{"c6i.large", "c7i.large"}, []string{"c6i.xlarge"}},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			re, err := regexp.Compile(patternToRegex(tt.pattern))
			if err != nil {
				t.Fatalf("patternToRegex(%q) produced invalid regex: %v", tt.pattern, err)
			}
			for _, m := range tt.match {
				if !re.MatchString(m) {
					t.Errorf("pattern %q should match %q (regex %q)", tt.pattern, m, re.String())
				}
			}
			for _, nm := range tt.noMatch {
				if re.MatchString(nm) {
					t.Errorf("pattern %q should NOT match %q (regex %q)", tt.pattern, nm, re.String())
				}
			}
		})
	}
}

func TestLooksLikeRegex(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"m7i*", false},
		{"m7i.large", false},
		{"c[6-8]i", true},
		{"(p4d|p5)", true},
		{"m7.+", true},
		{"\\d+", true},
	}
	for _, tt := range tests {
		if got := looksLikeRegex(tt.pattern); got != tt.want {
			t.Errorf("looksLikeRegex(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestPluralize(t *testing.T) {
	if got := pluralize(0, "region", "regions"); got != "regions" {
		t.Errorf("pluralize(0) = %q", got)
	}
	if got := pluralize(1, "region", "regions"); got != "region" {
		t.Errorf("pluralize(1) = %q", got)
	}
	if got := pluralize(5, "region", "regions"); got != "regions" {
		t.Errorf("pluralize(5) = %q", got)
	}
}

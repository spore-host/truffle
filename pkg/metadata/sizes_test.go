package metadata

import (
	"math"
	"regexp"
	"sort"
	"testing"
)

// liveSizes is every size DescribeInstanceTypes returned in us-west-2 and
// us-east-1 on 2026-10-15 (#220). TestEverySizeHasACategory checks each of
// them; TestLiveSizesHaveACategory (pkg/aws, TRUFFLE_LIVE_AWS=1) re-checks
// against the live catalog.
var liveSizes = []string{
	"nano", "micro", "small", "medium", "large", "xlarge",
	"2xlarge", "3xlarge", "4xlarge", "6xlarge", "8xlarge", "9xlarge",
	"10xlarge", "12xlarge", "16xlarge", "18xlarge", "24xlarge", "32xlarge",
	"48xlarge", "56xlarge", "64xlarge", "96xlarge", "112xlarge", "224xlarge",
	"metal", "metal-12xl", "metal-16xl", "metal-24xl", "metal-32xl",
	"metal-48xl", "metal-96xl",
}

func TestSizeCategoryOf(t *testing.T) {
	tests := map[string]string{
		"nano": "tiny", "micro": "tiny",
		"small": "small", "medium": "small",
		"large": "medium", "xlarge": "medium",
		"2xlarge": "large", "3xlarge": "large", "4xlarge": "large", "6xlarge": "large",
		"8xlarge": "huge", "9xlarge": "huge", "10xlarge": "huge", "24xlarge": "huge",
		"96xlarge": "huge", "224xlarge": "huge",
		"metal": "huge", "metal-12xl": "huge", "metal-24xl": "huge", "metal-48xl": "huge",
		// Not sizes.
		"": "", "xl": "", "0xlarge": "", "metal-": "", "jumbo": "", "24xl": "",
	}
	for size, want := range tests {
		if got := SizeCategoryOf(size); got != want {
			t.Errorf("SizeCategoryOf(%q) = %q, want %q", size, got, want)
		}
	}
}

// TestEverySizeHasACategory is the gate for #220: every size EC2 offers is in
// exactly one category, and the category's regex (what find actually
// matches with) agrees with SizeCategoryOf.
func TestEverySizeHasACategory(t *testing.T) {
	patterns := make(map[string]*regexp.Regexp)
	for name := range SizeCategories {
		p := SizePattern(name)
		if p == "" {
			t.Fatalf("SizePattern(%q) is empty", name)
		}
		patterns[name] = regexp.MustCompile("^" + p + "$")
	}
	for _, size := range liveSizes {
		cat := SizeCategoryOf(size)
		if cat == "" {
			t.Errorf("size %q is in no category, so no query word can select it", size)
			continue
		}
		var matched []string
		for name, re := range patterns {
			if re.MatchString(size) {
				matched = append(matched, name)
			}
		}
		sort.Strings(matched)
		if len(matched) != 1 || matched[0] != cat {
			t.Errorf("size %q: patterns match %v, want only %q", size, matched, cat)
		}
	}
}

// TestMetalSizesShareTheirShape: metal-Nxl is the bare-metal form of
// Nxlarge, so the two are always in the same category ("amd huge" listed
// c8a.24xlarge but not c8a.metal-24xl).
func TestMetalSizesShareTheirShape(t *testing.T) {
	for _, n := range []string{"2", "4", "12", "16", "24", "32", "48", "96"} {
		a, b := SizeCategoryOf(n+"xlarge"), SizeCategoryOf("metal-"+n+"xl")
		if a == "" || a != b {
			t.Errorf("%sxlarge is %q but metal-%sxl is %q", n, a, n, b)
		}
	}
}

// TestSizeCategoriesFollowSizeOrder: the ranges are contiguous and
// non-overlapping from 0 to +Inf, in the order of the words.
func TestSizeCategoriesFollowSizeOrder(t *testing.T) {
	order := []string{"tiny", "small", "medium", "large", "huge"}
	if len(order) != len(SizeCategories) {
		t.Fatalf("SizeCategories has %d entries, test knows %d", len(SizeCategories), len(order))
	}
	prevMax := 0.0
	for _, name := range order {
		c, ok := SizeCategories[name]
		if !ok {
			t.Fatalf("category %q missing", name)
		}
		if c.Name != name {
			t.Errorf("%q has Name %q", name, c.Name)
		}
		if c.Min != prevMax || !(c.Max > c.Min) {
			t.Errorf("%q range [%v, %v) does not follow the previous category ending at %v", name, c.Min, c.Max, prevMax)
		}
		prevMax = c.Max
	}
	if !math.IsInf(prevMax, 1) {
		t.Errorf("the largest category stops at %v, want no limit", prevMax)
	}
}

func TestSizePatternUnknownCategory(t *testing.T) {
	if p := SizePattern("nonexistent"); p != "" {
		t.Errorf("SizePattern(nonexistent) = %q, want empty", p)
	}
	if InSizeCategory("large", "") {
		t.Error(`InSizeCategory("large", "") = true`)
	}
}

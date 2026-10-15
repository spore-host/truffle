package metadata

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// SizeCategory is a query size word ("small", "huge", ...) and the range of
// instance sizes it selects.
//
// Membership is by size order, not by a list of names (#220): every instance
// size has a multiplier (see [SizeMultiplier]) and a category selects the
// sizes whose multiplier lies in [Min, Max). So a size EC2 adds later — a new
// 3xlarge, 96xlarge or metal-24xl — lands in the category its position says
// it belongs to, instead of in none.
type SizeCategory struct {
	Name string
	Min  float64 // smallest multiplier in the category (inclusive)
	Max  float64 // multiplier the category stops below (exclusive); +Inf for no limit
}

// SizeCategories maps size category names to their size ranges. Together the
// ranges cover every multiplier without overlap, so every instance size is in
// exactly one category.
var SizeCategories = map[string]SizeCategory{
	"tiny":   {Name: "tiny", Min: 0, Max: namedSizes["small"]},                    // nano, micro
	"small":  {Name: "small", Min: namedSizes["small"], Max: namedSizes["large"]}, // small, medium
	"medium": {Name: "medium", Min: namedSizes["large"], Max: 2},                  // large, xlarge
	"large":  {Name: "large", Min: 2, Max: 8},                                     // 2xlarge to 6xlarge
	"huge":   {Name: "huge", Min: 8, Max: math.Inf(1)},                            // 8xlarge and up, metal
}

// namedSizes are the multipliers of the sizes that have names rather than
// numbers, relative to xlarge = 1. Each is half the next.
var namedSizes = map[string]float64{
	"nano":   1.0 / 32,
	"micro":  1.0 / 16,
	"small":  1.0 / 8,
	"medium": 1.0 / 4,
	"large":  1.0 / 2,
	"xlarge": 1,
}

var (
	numberedSize = regexp.MustCompile(`^([1-9][0-9]*)xlarge$`)
	numberedMeta = regexp.MustCompile(`^metal-([1-9][0-9]*)xl$`)
)

// SizeMultiplier returns a size's position in the size order as a multiple
// of xlarge: "large" is 0.5, "xlarge" 1, "24xlarge" 24. "metal-24xl" is 24,
// the same shape as 24xlarge on bare metal, and a bare "metal" is +Inf, since
// it is the largest size of its family. ok is false for a size it doesn't
// recognise. size is the part after the dot ("24xlarge", not "c8a.24xlarge").
func SizeMultiplier(size string) (m float64, ok bool) {
	if v, ok := namedSizes[size]; ok {
		return v, true
	}
	if size == "metal" {
		return math.Inf(1), true
	}
	for _, re := range []*regexp.Regexp{numberedSize, numberedMeta} {
		if sm := re.FindStringSubmatch(size); sm != nil {
			n, err := strconv.Atoi(sm[1])
			if err != nil {
				return 0, false
			}
			return float64(n), true
		}
	}
	return 0, false
}

// SizeCategoryOf returns the category a size ("24xlarge", "metal-48xl") is
// in, or "" if the size isn't recognised.
func SizeCategoryOf(size string) string {
	m, ok := SizeMultiplier(size)
	if !ok {
		return ""
	}
	for name, cat := range SizeCategories {
		if m >= cat.Min && m < cat.Max {
			return name
		}
	}
	// Max is exclusive, so +Inf (bare metal) is caught here, in the category
	// with no upper limit.
	for name, cat := range SizeCategories {
		if math.IsInf(cat.Max, 1) && m >= cat.Min {
			return name
		}
	}
	return ""
}

// InSizeCategory reports whether size is in the named category.
func InSizeCategory(size, category string) bool {
	return category != "" && SizeCategoryOf(size) == category
}

// SizePattern returns a regular expression, without anchors, that matches
// exactly the sizes in category (for example "large" gives
// `(([2-7])xlarge|metal-([2-7])xl)`), or "" for an unknown category. It is
// derived from the category's range, so it matches numbered sizes no list
// names.
func SizePattern(category string) string {
	cat, ok := SizeCategories[category]
	if !ok {
		return ""
	}
	var alts []string
	for _, name := range []string{"nano", "micro", "small", "medium", "large", "xlarge"} {
		if m := namedSizes[name]; m >= cat.Min && m < cat.Max {
			alts = append(alts, name)
		}
	}
	// Numbered sizes start at 2xlarge; xlarge is the named size above.
	lo := int(math.Max(math.Ceil(cat.Min), 2))
	if num := integerRangePattern(lo, cat.Max); num != "" {
		alts = append(alts, "("+num+")xlarge", "metal-("+num+")xl")
	}
	if math.IsInf(cat.Max, 1) {
		alts = append(alts, "metal")
	}
	if len(alts) == 0 {
		return ""
	}
	return "(" + strings.Join(alts, "|") + ")"
}

// integerRangePattern returns a regex for the decimal integers n with
// lo <= n < hi (hi may be +Inf), or "" if there are none. Written for the
// small bounds SizeCategories uses: lo must be below 10, and a finite hi at
// most 10.
func integerRangePattern(lo int, hi float64) string {
	if float64(lo) >= hi {
		return ""
	}
	digits := func(a, b int) string { // a single digit in [a, b]
		if a == b {
			return strconv.Itoa(a)
		}
		return "[" + strconv.Itoa(a) + "-" + strconv.Itoa(b) + "]"
	}
	if math.IsInf(hi, 1) {
		return digits(lo, 9) + "|[1-9][0-9]+"
	}
	return digits(lo, int(hi)-1)
}

package cmd

import (
	"regexp"
	"testing"
)

// TestPatternToRegex_BareFamily: `truffle find g5` matched ^g5$, which no
// instance type is, and printed "No matching instance types found" (#229). A
// bare family name now means every size of that family, and only that family.
func TestPatternToRegex_BareFamily(t *testing.T) {
	for _, tt := range []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		{"g5", []string{"g5.xlarge", "g5.48xlarge"}, []string{"g5g.xlarge", "g5"}},
		{"C6I", []string{"c6i.large"}, []string{"c6in.large"}},
		{"m7i-flex", []string{"m7i-flex.large"}, []string{"m7i.large"}},
		{"m7i.large", []string{"m7i.large"}, []string{"m7i.xlarge"}}, // unchanged
	} {
		re := regexp.MustCompile(patternToRegex(tt.pattern))
		for _, it := range tt.match {
			if !re.MatchString(it) {
				t.Errorf("patternToRegex(%q) = %s does not match %s", tt.pattern, re, it)
			}
		}
		for _, it := range tt.noMatch {
			if re.MatchString(it) {
				t.Errorf("patternToRegex(%q) = %s matches %s", tt.pattern, re, it)
			}
		}
	}
}

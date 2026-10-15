package find

import "testing"

// TestSizeWordsMatchEveryNumberedSize is the find-level gate for #220: a size
// word selects every size in its range — numbered sizes like 3xlarge and
// 96xlarge, and metal-Nxl alongside its Nxlarge twin — with or without a
// family in the query.
func TestSizeWordsMatchEveryNumberedSize(t *testing.T) {
	tests := []struct {
		query     string
		match     []string
		dontMatch []string
	}{
		{"amd huge", []string{"c8a.24xlarge", "c8a.metal-24xl", "m8a.metal-48xl", "c8a.96xlarge", "m8a.48xlarge"}, []string{"c8a.4xlarge", "c8a.6xlarge"}},
		{"graviton huge", []string{"r8g.metal-24xl", "r8g.48xlarge", "c7g.metal"}, []string{"r8g.4xlarge"}},
		{"huge", []string{"x8i.96xlarge", "u7i-12tb.224xlarge", "m8a.metal-96xl", "c6i.9xlarge", "c5.18xlarge", "x2iezn.metal"}, []string{"m8a.6xlarge", "m8a.xlarge"}},
		{"large", []string{"m8azn.3xlarge", "c8a.6xlarge", "m7i.2xlarge", "m7i.4xlarge"}, []string{"m7i.xlarge", "m7i.8xlarge", "m7i.large", "m7i.metal"}},
		{"medium", []string{"m7i.large", "m7i.xlarge"}, []string{"m7i.2xlarge", "t3.medium"}},
	}
	for _, tt := range tests {
		pq, err := ParseQuery(tt.query)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", tt.query, err)
		}
		c, err := pq.BuildCriteria(false)
		if err != nil {
			t.Fatalf("BuildCriteria(%q): %v", tt.query, err)
		}
		// Family-qualified queries only match their families; the type names
		// above are chosen within them.
		for _, it := range tt.match {
			if !c.InstanceTypePattern.MatchString(it) {
				t.Errorf("%q does not match %s (pattern %s)", tt.query, it, c.InstanceTypePattern)
			}
		}
		for _, it := range tt.dontMatch {
			if c.InstanceTypePattern.MatchString(it) {
				t.Errorf("%q matches %s, which is outside its size range (pattern %s)", tt.query, it, c.InstanceTypePattern)
			}
		}
	}
}

package find

import (
	"strings"
	"testing"
)

// TestUnrecognisedQueryFailsFast is the gate for #229: a query with no word
// that narrows the search is an error naming the unknown words, not a search
// of the whole catalog.
func TestUnrecognisedQueryFailsFast(t *testing.T) {
	for _, q := range []string{"zzqx", "gravitn xlrage", "cheapest zzqx", "8"} {
		pq, err := ParseQuery(q)
		if err == nil {
			t.Errorf("ParseQuery(%q) = %+v, want an error", q, pq)
			continue
		}
		first := strings.Fields(q)[0]
		if q == "cheapest zzqx" {
			first = "zzqx"
		}
		if !strings.Contains(err.Error(), first) {
			t.Errorf("ParseQuery(%q) error %q does not name %q", q, err, first)
		}
	}
	// A suggestion for a near-miss.
	if _, err := ParseQuery("gravitn"); err == nil || !strings.Contains(err.Error(), `"graviton"`) {
		t.Errorf("ParseQuery(gravitn) error = %v, want a graviton suggestion", err)
	}
	// Recognised words still search, with or without unknown ones.
	for _, q := range []string{"graviton zzqx", "cheapest graviton", "xlarge"} {
		if _, err := ParseQuery(q); err != nil {
			t.Errorf("ParseQuery(%q) error = %v, want none", q, err)
		}
	}
}

// TestFamilyAndSizeNames: "g5 xlarge" parsed to nothing and searched every
// type (#229). A family name selects that family and a size name that size.
func TestFamilyAndSizeNames(t *testing.T) {
	tests := []struct {
		query   string
		match   []string
		noMatch []string
	}{
		{"g5 xlarge", []string{"g5.xlarge"}, []string{"g5.2xlarge", "m5.xlarge", "g5g.xlarge"}},
		{"g5", []string{"g5.xlarge", "g5.48xlarge"}, []string{"g5g.xlarge", "g6.xlarge"}},
		{"g5 g6 2xlarge", []string{"g5.2xlarge", "g6.2xlarge"}, []string{"g5.xlarge", "g6e.2xlarge"}},
		{"m7i-flex 2xlarge", []string{"m7i-flex.2xlarge"}, []string{"m7i.2xlarge"}},
		{"c8a metal-24xl", []string{"c8a.metal-24xl"}, []string{"c8a.24xlarge"}},
		{"amd m7a", []string{"m7a.large"}, []string{"m6a.large"}},
		{"g5 a10g", []string{"g5.xlarge"}, []string{"g6.xlarge"}},
		// A size word still means its range: large is 2xlarge to 6xlarge.
		{"g5 large", []string{"g5.2xlarge", "g5.4xlarge"}, []string{"g5.large", "g5.xlarge"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			sc, err := pq.BuildCriteria(false)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range tt.match {
				if !sc.InstanceTypePattern.MatchString(it) {
					t.Errorf("%q does not match %s (pattern %s)", tt.query, it, sc.InstanceTypePattern)
				}
			}
			for _, it := range tt.noMatch {
				if sc.InstanceTypePattern.MatchString(it) {
					t.Errorf("%q matches %s (pattern %s)", tt.query, it, sc.InstanceTypePattern)
				}
			}
		})
	}
}

// TestDisjointConstraintsMatchNothing: "intel h100" has no family in common
// (every H100 host has an AMD CPU), and matched every family instead, so it
// searched the whole x86 catalog (#229).
func TestDisjointConstraintsMatchNothing(t *testing.T) {
	for _, q := range []string{"intel h100", "g5 graviton", "g5 m7i graviton"} {
		pq, err := ParseQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		sc, err := pq.BuildCriteria(false)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range []string{"m6i.large", "p5.48xlarge", "g5.xlarge", "m7g.large", "m7i.large"} {
			if sc.InstanceTypePattern.MatchString(it) {
				t.Errorf("%q matches %s (pattern %s), want nothing", q, it, sc.InstanceTypePattern)
			}
		}
	}
}

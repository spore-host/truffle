package find

import (
	"slices"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// TestVendorQueriesSelectManufacturerFamilies: vendor and codename words
// reach the families #228 found missing, through the real parse and
// criteria path.
func TestVendorQueriesSelectManufacturerFamilies(t *testing.T) {
	tests := []struct {
		query   string
		match   []string // instance types the criteria must match
		noMatch []string
	}{
		// The demo query: x8aedz is Turin, and its 6xlarge (24 vCPU, 768 GiB)
		// meets the minimums.
		{"amd turin 16 cores 64gb", []string{"m8a.4xlarge", "x8aedz.6xlarge"}, []string{"m7a.4xlarge", "g5.4xlarge"}},
		{"amd", []string{"t3a.large", "x8aedz.large", "g5.xlarge", "p5.48xlarge"}, []string{"m6i.large", "m7g.large"}},
		{"intel", []string{"t3.large", "i4i.large", "m5zn.large", "d3.xlarge", "p5en.48xlarge"}, []string{"m6a.large", "m7g.large"}},
		// A vendor plus a GPU finds the hosts that have both.
		{"amd a10g", []string{"g5.xlarge"}, []string{"m6a.large"}},
		{"ice lake", []string{"i4i.large"}, []string{"x2iezn.2xlarge"}},
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
					t.Errorf("%q matches %s", tt.query, it)
				}
			}
		})
	}
}

// TestExplainMatchVendorOfUnresearchedFamily: a family with no codename entry
// (t8i) still explains its vendor match.
func TestExplainMatchVendorOfUnresearchedFamily(t *testing.T) {
	pq, err := ParseQuery("intel")
	if err != nil {
		t.Fatal(err)
	}
	reasons := ExplainMatch(aws.InstanceTypeResult{InstanceType: "t8i.large", Architecture: "x86_64"}, pq)
	if !slices.Contains(reasons, "Vendor: intel") {
		t.Errorf("reasons = %v, want Vendor: intel", reasons)
	}
}

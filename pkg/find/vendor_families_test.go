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
		// A bare vendor word selects CPU-only families.
		{"amd", []string{"t3a.large", "x8aedz.large"}, []string{"m6i.large", "m7g.large", "g5.xlarge", "p5.48xlarge"}},
		{"intel", []string{"t3.large", "i4i.large", "m5zn.large", "d3.xlarge"}, []string{"m6a.large", "m7g.large", "p5en.48xlarge", "g4dn.xlarge"}},
		// A vendor plus a GPU or accelerator finds the hosts that have both,
		// by the host CPU's maker.
		{"amd a10g", []string{"g5.xlarge", "g5.48xlarge"}, []string{"m6a.large", "g4dn.xlarge"}},
		{"intel h200", []string{"p5en.48xlarge"}, []string{"p5e.48xlarge", "m6i.large"}},
		{"intel trainium", []string{"trn1.32xlarge"}, []string{"m6i.large"}},
		{"amd 1 gpus", []string{"g5.xlarge", "p5.48xlarge"}, nil},
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

// gpuHostFamilies are the GPU and accelerator families with an AMD or Intel
// host CPU in the 2026-10-15 snapshot (GpuInfo, InferenceAcceleratorInfo,
// NeuronInfo, FpgaInfo or MediaAcceleratorInfo present).
var gpuHostFamilies = []string{
	"dl2q", "f2", "g4dn", "g5", "g6", "g6e", "g6f", "g7", "g7e", "gr6", "gr6f",
	"inf1", "inf2", "p3dn", "p4d", "p4de", "p5", "p5e", "p5en", "p6-b200",
	"p6-b300", "trn1", "trn1n", "trn2", "vt1",
}

// TestCPUWordsLeaveOutGPUHosts: a CPU word with no GPU or accelerator term
// selects no GPU/accelerator family (the user's decision on #228), so
// `amd 16 cores 64gb` lists CPU-only types.
func TestCPUWordsLeaveOutGPUHosts(t *testing.T) {
	for _, q := range []string{"amd 16 cores 64gb", "intel 16 cores 64gb", "graviton", "milan", "cascade lake", "avx-512", "amd turin 16 cores 64gb"} {
		pq, err := ParseQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		families := pq.ResolveInstanceFamilies()
		if len(families) == 0 {
			t.Errorf("%q resolved to no families", q)
		}
		for _, f := range families {
			if slices.Contains(gpuHostFamilies, f) || f == "g5g" {
				t.Errorf("%q selects GPU/accelerator family %s", q, f)
			}
		}
	}
}

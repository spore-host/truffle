package metadata

import (
	"testing"
)

func TestProcessorDatabase(t *testing.T) {
	tests := []struct {
		name         string
		codeName     string
		wantVendor   string
		wantArch     string
		wantFamilies int // minimum expected families
	}{
		{
			name:         "ice lake",
			codeName:     "ice lake",
			wantVendor:   "intel",
			wantArch:     "x86_64",
			wantFamilies: 3,
		},
		{
			name:         "milan",
			codeName:     "milan",
			wantVendor:   "amd",
			wantArch:     "x86_64",
			wantFamilies: 3,
		},
		{
			name:         "graviton2",
			codeName:     "graviton2",
			wantVendor:   "aws",
			wantArch:     "arm64",
			wantFamilies: 5,
		},
		{
			name:         "sapphire rapids",
			codeName:     "sapphire rapids",
			wantVendor:   "intel",
			wantArch:     "x86_64",
			wantFamilies: 3,
		},
		{
			name:         "genoa",
			codeName:     "genoa",
			wantVendor:   "amd",
			wantArch:     "x86_64",
			wantFamilies: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, ok := ProcessorDatabase[tt.codeName]
			if !ok {
				t.Fatalf("processor %q not found in database", tt.codeName)
			}

			if info.Vendor != tt.wantVendor {
				t.Errorf("vendor = %v, want %v", info.Vendor, tt.wantVendor)
			}

			if info.Architecture != tt.wantArch {
				t.Errorf("architecture = %v, want %v", info.Architecture, tt.wantArch)
			}

			if len(info.Families) < tt.wantFamilies {
				t.Errorf("families count = %v, want >= %v", len(info.Families), tt.wantFamilies)
			}
		})
	}
}

func TestVendorAliases(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"intel", "intel"},
		{"amd", "amd"},
		{"aws", "aws"},
		{"graviton", "aws"},
		{"arm", "aws"},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, ok := VendorAliases[tt.alias]
			if !ok {
				t.Fatalf("alias %q not found", tt.alias)
			}
			if got != tt.want {
				t.Errorf("VendorAliases[%q] = %v, want %v", tt.alias, got, tt.want)
			}
		})
	}
}

func TestGetProcessorsByVendor(t *testing.T) {
	tests := []struct {
		vendor  string
		wantMin int // minimum expected processors
	}{
		{"intel", 3},
		{"amd", 4},
		{"aws", 4},
	}

	for _, tt := range tests {
		t.Run(tt.vendor, func(t *testing.T) {
			processors := GetProcessorsByVendor(tt.vendor)
			if len(processors) < tt.wantMin {
				t.Errorf("GetProcessorsByVendor(%q) returned %d processors, want >= %d",
					tt.vendor, len(processors), tt.wantMin)
			}
		})
	}
}

func TestGetFamiliesByVendor(t *testing.T) {
	tests := []struct {
		vendor  string
		wantMin int // minimum expected families
	}{
		{"intel", 10},
		{"amd", 10},
		{"aws", 10},
	}

	for _, tt := range tests {
		t.Run(tt.vendor, func(t *testing.T) {
			families := GetFamiliesByVendor(tt.vendor)
			if len(families) < tt.wantMin {
				t.Errorf("GetFamiliesByVendor(%q) returned %d families, want >= %d",
					tt.vendor, len(families), tt.wantMin)
			}
		})
	}
}

// awsManufacturedFamilies is every instance family whose
// ProcessorInfo.Manufacturer was "AWS" in us-west-2 and us-east-1
// DescribeInstanceTypes on 2026-10-15 (#221).
var awsManufacturedFamilies = []string{
	"a1", "c6g", "c6gd", "c6gn", "c7g", "c7gd", "c7gn", "c8g", "c8gb", "c8gd",
	"c8gn", "c9g", "c9gd", "g5g", "hpc7g", "i4g", "i8g", "i8ge", "im4gn",
	"is4gen", "m6g", "m6gd", "m7g", "m7gd", "m8g", "m8gb", "m8gd", "m8gn",
	"m9g", "m9gd", "r6g", "r6gd", "r7g", "r7gd", "r8g", "r8gb", "r8gd", "r8gn",
	"r9g", "r9gd", "t4g", "x2gd", "x8g",
}

// TestGravitonListsCoverEveryAWSFamily is the gate for #221: every
// Graviton family EC2 offers is in a Graviton processor entry, so "graviton"
// (and "arm", "aws") queries select it. r9g and r9gd were missing.
func TestGravitonListsCoverEveryAWSFamily(t *testing.T) {
	have := make(map[string]bool)
	for _, f := range GetFamiliesByVendor("aws") {
		have[f] = true
	}
	for _, f := range awsManufacturedFamilies {
		if !have[f] {
			t.Errorf("Graviton family %s is in no Graviton processor entry", f)
		}
	}
}

// TestGraviton5Families: r9g/r9gd are Graviton5, alongside m9g and c9g.
func TestGraviton5Families(t *testing.T) {
	got := make(map[string]bool)
	for _, f := range ProcessorDatabase["graviton5"].Families {
		got[f] = true
	}
	for _, f := range []string{"m9g", "m9gd", "c9g", "c9gd", "r9g", "r9gd"} {
		if !got[f] {
			t.Errorf("graviton5 is missing %s", f)
		}
	}
}

// amdManufacturedFamilies and intelManufacturedFamilies are every instance
// family whose ProcessorInfo.Manufacturer was "AMD" or "Intel" in us-west-2,
// us-east-1, us-east-2, eu-west-1 and ap-northeast-1 DescribeInstanceTypes on
// 2026-10-15 (#228), GPU and accelerator hosts included.
var amdManufacturedFamilies = []string{
	"c5a", "c5ad", "c6a", "c7a", "c8a", "f2", "g5", "g6", "g6e", "g6f", "gr6",
	"gr6f", "hpc6a", "hpc7a", "hpc8a", "inf2", "m5a", "m5ad", "m6a", "m7a",
	"m8a", "m8azn", "p5", "p5e", "r5a", "r5ad", "r6a", "r7a", "r8a", "t3a",
	"x8aedz",
}

var intelManufacturedFamilies = []string{
	"c1", "c3", "c4", "c5", "c5d", "c5n", "c6i", "c6id", "c6in", "c7i",
	"c7i-flex", "c8i", "c8i-flex", "c8ib", "c8id", "c8in", "c8ine", "d2", "d3",
	"d3en", "dl2q", "g4dn", "g7", "g7e", "h1", "hpc6id", "i2", "i3", "i3en",
	"i4i", "i7i", "i7ie", "inf1", "m1", "m2", "m3", "m4", "m5", "m5d", "m5dn",
	"m5n", "m5zn", "m6i", "m6id", "m6idn", "m6in", "m7i", "m7i-flex", "m8i",
	"m8i-flex", "m8ib", "m8id", "m8idb", "m8idn", "m8in", "m8ine", "mac1",
	"p3dn", "p4d", "p4de", "p5en", "p6-b200", "p6-b300", "r3", "r4", "r5",
	"r5b", "r5d", "r5dn", "r5n", "r6i", "r6id", "r6idn", "r6in", "r7i", "r7iz",
	"r8i", "r8i-flex", "r8ib", "r8id", "r8idb", "r8idn", "r8in", "t1", "t2",
	"t3", "t8i", "trn1", "trn1n", "trn2", "u-3tb1", "u-6tb1", "u7i-12tb",
	"u7i-6tb", "u7i-8tb", "u7in-16tb", "u7in-24tb", "u7in-32tb", "vt1", "x1",
	"x1e", "x2idn", "x2iedn", "x2iezn", "x8i", "z1d",
}

// recordedManufacturer returns the vendor each family's recorded
// ProcessorInfo.Manufacturer maps to.
func recordedManufacturer() map[string]string {
	m := make(map[string]string)
	for _, f := range amdManufacturedFamilies {
		m[f] = "amd"
	}
	for _, f := range intelManufacturedFamilies {
		m[f] = "intel"
	}
	for _, f := range awsManufacturedFamilies {
		m[f] = "aws"
	}
	return m
}

// TestVendorFamiliesMatchManufacturer is the gate for #228: each vendor word
// selects exactly the families whose recorded ProcessorInfo.Manufacturer is
// that vendor, GPU/accelerator hosts included (by decision: a vendor word
// names the host CPU's maker). t3a, x8aedz, i4i, m5zn, t3, d3 and the GPU
// hosts were in no AMD or Intel list before.
func TestVendorFamiliesMatchManufacturer(t *testing.T) {
	recorded := recordedManufacturer()
	for _, vendor := range []string{"amd", "intel", "aws"} {
		have := make(map[string]bool)
		for _, f := range GetFamiliesByVendor(vendor) {
			have[f] = true
			if maker, ok := recorded[f]; ok && maker != vendor {
				t.Errorf("%s is listed under %s but its processor is made by %s", f, vendor, maker)
			}
		}
		for f, maker := range recorded {
			if maker == vendor && !have[f] {
				t.Errorf("%s has a %s processor but %q does not select it", f, maker, vendor)
			}
		}
	}
}

// TestCodenameEntriesAgreeWithManufacturer: no codename entry lists a family
// whose recorded processor is made by a different vendor.
func TestCodenameEntriesAgreeWithManufacturer(t *testing.T) {
	recorded := recordedManufacturer()
	for key, info := range ProcessorDatabase {
		for _, f := range info.Families {
			if maker, ok := recorded[f]; ok && maker != info.Vendor {
				t.Errorf("%q (%s) lists %s, whose processor is made by %s", key, info.Vendor, f, maker)
			}
		}
	}
}

// TestCodenameFamilies228 pins the codename assignments researched for #228.
func TestCodenameFamilies228(t *testing.T) {
	has := func(key, family string) bool {
		for _, f := range ProcessorDatabase[key].Families {
			if f == family {
				return true
			}
		}
		return false
	}
	want := map[string][]string{
		"turin":           {"x8aedz", "hpc8a"},
		"zen 5":           {"x8aedz", "hpc8a"},
		"milan":           {"g6", "g6e", "p5", "inf2", "f2"},
		"rome":            {"g5"},
		"ice lake":        {"i4i", "trn1", "hpc6id"},
		"cascade lake":    {"m5zn", "x2iezn", "d3", "d3en", "g4dn", "p4d"},
		"skylake":         {"u-3tb1", "u-6tb1", "p3dn"},
		"sapphire rapids": {"p5en", "trn2"},
		"emerald rapids":  {"p6-b200"},
		"broadwell":       {"h1", "i3"},
		"haswell":         {"x1", "x1e"},
	}
	for key, families := range want {
		for _, f := range families {
			if !has(key, f) {
				t.Errorf("%q is missing %s", key, f)
			}
		}
	}
	// x2iezn is Cascade Lake (Xeon 8252C), not Ice Lake.
	if has("ice lake", "x2iezn") {
		t.Error(`"ice lake" lists x2iezn, which is Cascade Lake`)
	}
}

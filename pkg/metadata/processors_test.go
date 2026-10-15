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

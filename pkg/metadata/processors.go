package metadata

// ProcessorInfo contains information about a CPU processor used in EC2 instances
type ProcessorInfo struct {
	CodeName     string   // "ice lake", "milan", "sapphire rapids"
	Vendor       string   // "intel", "amd", "aws"
	Architecture string   // "x86_64", "arm64"
	Generation   string   // "3rd gen", "4th gen"
	Families     []string // ["m6i", "c6i", "r6i"]
}

// ProcessorDatabase maps processor code names to their information.
//
// Family lists are cross-checked against live us-east-1 DescribeInstanceTypes
// output (2026-08) plus per-family processor listings (instances.vantage.sh),
// since AWS's own API exposes no processor codename field. Every AZ/storage/
// network variant suffix (-d, -n, -dn, -a, -e, -flex, -b, -in, -ine, -zn, ...)
// that shares its parent family's silicon is included explicitly — a prior
// version of this table only listed the base family, which silently excluded
// most of a generation's real instance types from processor/instruction-set
// searches.
var ProcessorDatabase = map[string]ProcessorInfo{
	// Intel processors
	"granite rapids": {
		CodeName:     "Granite Rapids",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "6th gen",
		Families:     []string{"m8i", "m8i-flex", "m8ib", "m8id", "m8idb", "m8idn", "m8in", "m8ine", "c8i", "c8i-flex", "c8ib", "c8id", "c8in", "c8ine", "r8i", "r8i-flex", "r8ib", "r8id", "r8idb", "r8idn", "r8in", "x8i"},
	},
	"emerald rapids": {
		CodeName:     "Emerald Rapids",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "5th gen",
		Families:     []string{"i7i", "i7ie"},
	},
	"sapphire rapids": {
		CodeName:     "Sapphire Rapids",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "4th gen",
		Families:     []string{"m7i", "c7i", "r7i", "r7iz", "m7i-flex", "c7i-flex", "u7i-6tb", "u7i-8tb", "u7i-12tb", "u7in-16tb", "u7in-24tb", "u7in-32tb"},
	},
	"ice lake": {
		CodeName:     "Ice Lake",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "3rd gen",
		Families:     []string{"m6i", "m6id", "m6idn", "m6in", "c6i", "c6id", "c6in", "r6i", "r6id", "r6idn", "r6in", "x2idn", "x2iedn", "x2iezn"},
	},
	"cascade lake": {
		CodeName:     "Cascade Lake",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "2nd gen",
		Families:     []string{"m5", "c5", "r5", "m5n", "m5dn", "c5n", "r5n", "r5dn", "m5d", "c5d", "r5d", "r5b"},
	},
	"skylake": {
		CodeName:     "Skylake",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "1st gen",
		Families:     []string{"m5", "c5", "r5", "z1d"},
	},
	"haswell": {
		CodeName:     "Haswell",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "legacy",
		Families:     []string{"m4", "c4", "r4", "t2", "d2", "i2"},
	},
	"broadwell": {
		CodeName:     "Broadwell",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "legacy",
		Families:     []string{"m4", "c4", "t2", "d2"},
	},
	"ivy bridge": {
		CodeName:     "Ivy Bridge",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "legacy",
		Families:     []string{"m3", "c3", "r3", "i2"},
	},
	"sandy bridge": {
		CodeName:     "Sandy Bridge",
		Vendor:       "intel",
		Architecture: "x86_64",
		Generation:   "legacy",
		Families:     []string{"m1", "c1", "m2", "t1"},
	},

	// AMD processors
	"turin": {
		CodeName:     "Turin",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "5th gen",
		Families:     []string{"m8a", "m8azn", "c8a", "r8a"},
	},
	"genoa": {
		CodeName:     "Genoa",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "4th gen",
		Families:     []string{"m7a", "c7a", "r7a", "hpc7a"},
	},
	"bergamo": {
		CodeName:     "Bergamo",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "4th gen",
		Families:     []string{"m7a", "c7a"},
	},
	"milan": {
		CodeName:     "Milan",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "3rd gen",
		Families:     []string{"m6a", "c6a", "r6a", "hpc6a"},
	},
	"rome": {
		CodeName:     "Rome",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "2nd gen",
		Families:     []string{"m5a", "c5a", "r5a", "m5ad", "c5ad", "r5ad"},
	},
	"zen 3": {
		CodeName:     "Zen 3",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "3rd gen",
		Families:     []string{"m6a", "c6a", "r6a", "hpc6a"},
	},
	"zen 4": {
		CodeName:     "Zen 4",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "4th gen",
		Families:     []string{"m7a", "c7a", "r7a", "hpc7a"},
	},
	"zen 5": {
		CodeName:     "Zen 5",
		Vendor:       "amd",
		Architecture: "x86_64",
		Generation:   "5th gen",
		Families:     []string{"m8a", "m8azn", "c8a", "r8a"},
	},

	// AWS Graviton processors
	"graviton": {
		CodeName:     "Graviton",
		Vendor:       "aws",
		Architecture: "arm64",
		Generation:   "1st gen",
		Families:     []string{"a1"},
	},
	"graviton2": {
		CodeName:     "Graviton2",
		Vendor:       "aws",
		Architecture: "arm64",
		Generation:   "2nd gen",
		Families:     []string{"m6g", "m6gd", "c6g", "c6gd", "c6gn", "r6g", "r6gd", "t4g", "x2gd", "im4gn", "is4gen", "i4g"},
	},
	"graviton3": {
		CodeName:     "Graviton3",
		Vendor:       "aws",
		Architecture: "arm64",
		Generation:   "3rd gen",
		Families:     []string{"m7g", "m7gd", "c7g", "c7gd", "c7gn", "r7g", "r7gd", "hpc7g", "g5g"},
	},
	"graviton3e": {
		CodeName:     "Graviton3E",
		Vendor:       "aws",
		Architecture: "arm64",
		Generation:   "3rd gen",
		Families:     []string{"c7gn", "hpc7g"},
	},
	"graviton4": {
		CodeName:     "Graviton4",
		Vendor:       "aws",
		Architecture: "arm64",
		Generation:   "4th gen",
		Families:     []string{"m8g", "m8gb", "m8gd", "m8gn", "c8g", "c8gb", "c8gd", "c8gn", "r8g", "r8gb", "r8gd", "r8gn", "x8g", "i8g", "i8ge"},
	},
	"graviton5": {
		CodeName:     "Graviton5",
		Vendor:       "aws",
		Architecture: "arm64",
		Generation:   "5th gen",
		Families:     []string{"m9g", "m9gd", "c9g", "c9gd"},
	},
}

// VendorAliases maps common vendor names to canonical forms
var VendorAliases = map[string]string{
	"intel":    "intel",
	"amd":      "amd",
	"aws":      "aws",
	"graviton": "aws",
	"arm":      "aws",
	"amazon":   "aws",
}

// GetProcessorsByVendor returns all processors for a given vendor
func GetProcessorsByVendor(vendor string) []ProcessorInfo {
	var processors []ProcessorInfo
	for _, info := range ProcessorDatabase {
		if info.Vendor == vendor {
			processors = append(processors, info)
		}
	}
	return processors
}

// GetFamiliesByVendor returns all instance families for a given vendor
func GetFamiliesByVendor(vendor string) []string {
	familySet := make(map[string]bool)
	for _, info := range ProcessorDatabase {
		if info.Vendor == vendor {
			for _, family := range info.Families {
				familySet[family] = true
			}
		}
	}

	families := make([]string, 0, len(familySet))
	for family := range familySet {
		families = append(families, family)
	}
	return families
}

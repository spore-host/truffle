package metadata

// familyVendors maps each instance family to the maker of its host CPU, as
// DescribeInstanceTypes reports it in ProcessorInfo.Manufacturer ("AMD" ->
// "amd", "Intel" -> "intel", "AWS" -> "aws"). It is a snapshot of us-west-2,
// us-east-1, us-east-2, eu-west-1 and ap-northeast-1 taken 2026-10-15 (#228),
// and it is what the vendor words (amd, intel, graviton/aws) select. Set
// TRUFFLE_LIVE_AWS=1 to compare it with the live catalog
// (TestLiveVendorFamiliesAreListed in pkg/aws).
//
// Vendor membership comes from this table rather than from the codename
// entries in ProcessorDatabase because the API reports the manufacturer but
// not the codename: a family can be put under its vendor from the API alone,
// while its codename (turin, ice lake, ...) needs per-family research. Before
// #228, vendor queries used only the codename lists, so a family nobody had
// researched (t3a, t3, i4i, m5zn, d3, x8aedz, ...) matched no vendor word.
//
// GPU and accelerator hosts are listed under their host CPU's maker, which is
// what AWS reports: g5, g6 and p5 are AMD, p4d, p5en and trn1 are Intel. find
// uses that only when the query also names a GPU or accelerator ("amd a10g"
// finds g5); a bare vendor word selects CPU-only families (see
// acceleratorFamilies). Apple (mac2, mac-m4, ...) has no vendor word and is
// left out; mac1 is Intel.
var familyVendors = map[string]string{
	// AMD
	"c5a": "amd", "c5ad": "amd", "c6a": "amd", "c7a": "amd", "c8a": "amd",
	"f2": "amd", "g5": "amd", "g6": "amd", "g6e": "amd", "g6f": "amd",
	"gr6": "amd", "gr6f": "amd", "hpc6a": "amd", "hpc7a": "amd", "hpc8a": "amd",
	"inf2": "amd", "m5a": "amd", "m5ad": "amd", "m6a": "amd", "m7a": "amd",
	"m8a": "amd", "m8azn": "amd", "p5": "amd", "p5e": "amd", "r5a": "amd",
	"r5ad": "amd", "r6a": "amd", "r7a": "amd", "r8a": "amd", "t3a": "amd",
	"x8aedz": "amd",

	// Intel
	"c1": "intel", "c3": "intel", "c4": "intel", "c5": "intel", "c5d": "intel",
	"c5n": "intel", "c6i": "intel", "c6id": "intel", "c6in": "intel",
	"c7i": "intel", "c7i-flex": "intel", "c8i": "intel", "c8i-flex": "intel",
	"c8ib": "intel", "c8id": "intel", "c8in": "intel", "c8ine": "intel",
	"d2": "intel", "d3": "intel", "d3en": "intel", "dl2q": "intel",
	"g4dn": "intel", "g7": "intel", "g7e": "intel", "h1": "intel",
	"hpc6id": "intel", "i2": "intel", "i3": "intel", "i3en": "intel",
	"i4i": "intel", "i7i": "intel", "i7ie": "intel", "inf1": "intel",
	"m1": "intel", "m2": "intel", "m3": "intel", "m4": "intel", "m5": "intel",
	"m5d": "intel", "m5dn": "intel", "m5n": "intel", "m5zn": "intel",
	"m6i": "intel", "m6id": "intel", "m6idn": "intel", "m6in": "intel",
	"m7i": "intel", "m7i-flex": "intel", "m8i": "intel", "m8i-flex": "intel",
	"m8ib": "intel", "m8id": "intel", "m8idb": "intel", "m8idn": "intel",
	"m8in": "intel", "m8ine": "intel", "mac1": "intel", "p3dn": "intel",
	"p4d": "intel", "p4de": "intel", "p5en": "intel", "p6-b200": "intel",
	"p6-b300": "intel", "r3": "intel", "r4": "intel", "r5": "intel",
	"r5b": "intel", "r5d": "intel", "r5dn": "intel", "r5n": "intel",
	"r6i": "intel", "r6id": "intel", "r6idn": "intel", "r6in": "intel",
	"r7i": "intel", "r7iz": "intel", "r8i": "intel", "r8i-flex": "intel",
	"r8ib": "intel", "r8id": "intel", "r8idb": "intel", "r8idn": "intel",
	"r8in": "intel", "t1": "intel", "t2": "intel", "t3": "intel", "t8i": "intel",
	"trn1": "intel", "trn1n": "intel", "trn2": "intel", "u-3tb1": "intel",
	"u-6tb1": "intel", "u7i-12tb": "intel", "u7i-6tb": "intel",
	"u7i-8tb": "intel", "u7in-16tb": "intel", "u7in-24tb": "intel",
	"u7in-32tb": "intel", "vt1": "intel", "x1": "intel", "x1e": "intel",
	"x2idn": "intel", "x2iedn": "intel", "x2iezn": "intel", "x8i": "intel",
	"z1d": "intel",

	// AWS (Graviton)
	"a1": "aws", "c6g": "aws", "c6gd": "aws", "c6gn": "aws", "c7g": "aws",
	"c7gd": "aws", "c7gn": "aws", "c8g": "aws", "c8gb": "aws", "c8gd": "aws",
	"c8gn": "aws", "c9g": "aws", "c9gd": "aws", "g5g": "aws", "hpc7g": "aws",
	"i4g": "aws", "i8g": "aws", "i8ge": "aws", "im4gn": "aws", "is4gen": "aws",
	"m6g": "aws", "m6gd": "aws", "m7g": "aws", "m7gd": "aws", "m8g": "aws",
	"m8gb": "aws", "m8gd": "aws", "m8gn": "aws", "m9g": "aws", "m9gd": "aws",
	"r6g": "aws", "r6gd": "aws", "r7g": "aws", "r7gd": "aws", "r8g": "aws",
	"r8gb": "aws", "r8gd": "aws", "r8gn": "aws", "r9g": "aws", "r9gd": "aws",
	"t4g": "aws", "x2gd": "aws", "x8g": "aws",
}

// VendorOfFamily returns the vendor ("amd", "intel" or "aws") of a family's
// host CPU, or "" if the family is unknown: from familyVendors, else from the
// codename entry that lists it.
func VendorOfFamily(family string) string {
	if v, ok := familyVendors[family]; ok {
		return v
	}
	for _, info := range ProcessorDatabase {
		for _, f := range info.Families {
			if f == family {
				return info.Vendor
			}
		}
	}
	return ""
}

// acceleratorFamilies is every instance family with a GPU, inference
// accelerator, Neuron device, FPGA or media accelerator: the families whose
// DescribeInstanceTypes entry had GpuInfo, InferenceAcceleratorInfo,
// NeuronInfo, FpgaInfo or MediaAcceleratorInfo in the same five-region
// snapshot as familyVendors (2026-10-15). TRUFFLE_LIVE_AWS=1 compares it with
// the live catalog (TestLiveAcceleratorFamilies in pkg/aws).
//
// CPU words (vendor, processor codename, instruction set) leave these
// families out unless the query also names a GPU or accelerator, so
// `amd 16 cores 64gb` lists CPU-only types while `amd a10g` still finds g5
// (#228).
var acceleratorFamilies = map[string]bool{
	"dl2q": true, "f2": true, "g4dn": true, "g5": true, "g5g": true, "g6": true,
	"g6e": true, "g6f": true, "g7": true, "g7e": true, "gr6": true, "gr6f": true,
	"inf1": true, "inf2": true, "p3dn": true, "p4d": true, "p4de": true,
	"p5": true, "p5e": true, "p5en": true, "p6-b200": true, "p6-b300": true,
	"trn1": true, "trn1n": true, "trn2": true, "vt1": true,
}

// IsAcceleratorFamily reports whether family's instances carry a GPU or
// other accelerator (see acceleratorFamilies).
func IsAcceleratorFamily(family string) bool {
	return acceleratorFamilies[family]
}

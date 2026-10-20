package metadata

import "sort"

// instanceFamilies is every instance family DescribeInstanceTypes reported in
// us-west-2, us-east-1, us-east-2, eu-west-1 and ap-northeast-1 on 2026-10-15
// (#229). It lets find recognise a bare family name ("g5", "m7i-flex") as a
// query word. Set TRUFFLE_LIVE_AWS=1 to compare it with the live catalog
// (TestLiveFamiliesAreRecognised in pkg/aws); a family offered only in other
// regions is still recognised if the Nitro table (pkg/aws) lists it.
var instanceFamilies = map[string]bool{}

func init() {
	for _, f := range []string{
		"a1", "c1", "c3", "c4", "c5", "c5a", "c5ad", "c5d", "c5n", "c6a",
		"c6g", "c6gd", "c6gn", "c6i", "c6id", "c6in", "c7a", "c7g", "c7gd",
		"c7gn", "c7i", "c7i-flex", "c8a", "c8g", "c8gb", "c8gd", "c8gn",
		"c8i", "c8i-flex", "c8ib", "c8id", "c8in", "c8ine", "c9g", "c9gd",
		"d2", "d3", "d3en", "dl2q", "f2", "g4dn", "g5", "g5g", "g6", "g6e",
		"g6f", "g7", "g7e", "gr6", "gr6f", "h1", "hpc6a", "hpc6id", "hpc7a",
		"hpc7g", "hpc8a", "i2", "i3", "i3en", "i4g", "i4i", "i7i", "i7ie",
		"i8g", "i8ge", "im4gn", "inf1", "inf2", "is4gen", "m1", "m2", "m3",
		"m4", "m5", "m5a", "m5ad", "m5d", "m5dn", "m5n", "m5zn", "m6a",
		"m6g", "m6gd", "m6i", "m6id", "m6idn", "m6in", "m7a", "m7g", "m7gd",
		"m7i", "m7i-flex", "m8a", "m8azn", "m8g", "m8gb", "m8gd", "m8gn",
		"m8i", "m8i-flex", "m8ib", "m8id", "m8idb", "m8idn", "m8in", "m8ine",
		"m9g", "m9gd", "mac-m3ultra", "mac-m4", "mac-m4max", "mac-m4pro",
		"mac1", "mac2", "mac2-m1ultra", "mac2-m2", "mac2-m2pro", "p3dn",
		"p4d", "p4de", "p5", "p5e", "p5en", "p6-b200", "p6-b300", "r3", "r4",
		"r5", "r5a", "r5ad", "r5b", "r5d", "r5dn", "r5n", "r6a", "r6g",
		"r6gd", "r6i", "r6id", "r6idn", "r6in", "r7a", "r7g", "r7gd", "r7i",
		"r7iz", "r8a", "r8g", "r8gb", "r8gd", "r8gn", "r8i", "r8i-flex",
		"r8ib", "r8id", "r8idb", "r8idn", "r8in", "r9g", "r9gd", "t1", "t2",
		"t3", "t3a", "t4g", "t8i", "trn1", "trn1n", "trn2", "u-3tb1",
		"u-6tb1", "u7i-12tb", "u7i-6tb", "u7i-8tb", "u7in-16tb", "u7in-24tb",
		"u7in-32tb", "vt1", "x1", "x1e", "x2gd", "x2idn", "x2iedn", "x2iezn",
		"x8aedz", "x8g", "x8i", "z1d",
	} {
		instanceFamilies[f] = true
	}
}

// IsInstanceFamily reports whether family is a known instance family name
// ("g5", "m7i-flex", "u7in-16tb"), from the 2026-10-15 catalog snapshot.
func IsInstanceFamily(family string) bool {
	return instanceFamilies[family]
}

// InstanceFamilies returns every known instance family name, sorted: the
// snapshot [IsInstanceFamily] consults. It exists so tests can enumerate the
// families instead of listing them (#263).
func InstanceFamilies() []string {
	out := make([]string, 0, len(instanceFamilies))
	for f := range instanceFamilies {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

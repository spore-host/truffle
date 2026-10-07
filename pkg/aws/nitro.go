package aws

import (
	"strings"
)

// NitroGenerationSource and NitroGenerationAsOf record where this table came
// from and when, so its staleness is a fact in the file rather than a guess.
const (
	NitroGenerationSource = "https://docs.aws.amazon.com/ec2/latest/instancetypes/ec2-nitro-instances.html"
	NitroGenerationAsOf   = "2026-10-07"
)

// NitroGenerationUnknown is returned for a family this table does not classify.
// It is a first-class answer, never a guess: a caller that cannot tell "we do
// not know" from "it is v2" would make the same mistake the static price table
// makes for spend caps (#175).
const NitroGenerationUnknown = 0

// nitroGenerationByFamily maps an instance family to its Nitro System version.
//
// WHY A HAND-MAINTAINED TABLE, when this project argues against them.
//
// EC2 does expose a Hypervisor field on DescribeInstanceTypes — but its only
// values are "nitro" and "xen" (InstanceTypeHypervisor in the SDK). The Nitro
// VERSION appears nowhere in the API; AWS publishes it only in documentation,
// in the Hypervisor column of each family's Platform summary table. So a table
// is the only way to answer "which Nitro generation", and the question is worth
// answering: v4 added ENA Express and RDMA, v5 raised the per-card ceiling to
// 200 Gbps, v6 to 400 Gbps and cut the idle TCP timeout from 432,000s to 350s —
// which silently breaks long-lived idle connections.
//
// HOW IT STAYS HONEST, which is the part that matters. Three mechanisms, because
// a table nobody checks is the failure mode this project keeps hitting:
//
//  1. Immutability. A family's Nitro version is fixed when the family launches.
//     Unlike a price, an existing entry cannot silently become wrong — only
//     MISSING entries accrue. Missing is detectable; silently wrong is not.
//  2. A contradiction gate. Every family here must be reported as "nitro" by
//     DescribeInstanceTypes. That validates the one dimension the API can
//     validate, automatically, against AWS.
//  3. A coverage gate. Every family EC2 currently offers with Hypervisor=nitro
//     must appear here, or the check fails and names it. This is the
//     new-Nitro-card detector: a new card always arrives with new families, so
//     an unclassified family is exactly the signal to go read the docs. It
//     cannot tell you the card is v7 — nothing can, from the API — but it fires
//     precisely when someone needs to look.
//
// Run both networked gates with: make nitro-census
//
// The AWS page's lowest listed generation is v2; there is no v1 section, so
// nothing here claims one.
//
// NOT INCLUDED: i3. The source lists "I3" under Nitro v2 BARE METAL only, and
// every virtualized i3 size reports hypervisor=xen. A family-level key cannot
// express "metal sizes only", and it does not need to: the Hypervisor field is
// per-instance-type and authoritative, so i3.large correctly reports xen while
// this table correctly declines to claim a generation. The contradiction gate
// found this on its first run against AWS.
var nitroGenerationByFamily = map[string]int{
	// --- Nitro v6 ---
	"c8a":      6,
	"c8gb":     6,
	"c8gn":     6,
	"c8i":      6,
	"c8i-flex": 6,
	"c8ib":     6,
	"c8id":     6,
	"c8in":     6,
	"c8ine":    6,
	"c9g":      6,
	"c9gd":     6,
	"g7":       6,
	"g7e":      6,
	"hpc8a":    6,
	"i8ge":     6,
	"m8a":      6,
	"m8azn":    6,
	"m8gb":     6,
	"m8gn":     6,
	"m8i":      6,
	"m8i-flex": 6,
	"m8ib":     6,
	"m8id":     6,
	"m8idb":    6,
	"m8idn":    6,
	"m8in":     6,
	"m8ine":    6,
	"m9g":      6,
	"m9gd":     6,
	"p6-b200":  6,
	"p6-b300":  6,
	"r8a":      6,
	"r8gb":     6,
	"r8gn":     6,
	"r8i":      6,
	"r8i-flex": 6,
	"r8ib":     6,
	"r8id":     6,
	"r8idb":    6,
	"r8idn":    6,
	"r8in":     6,
	"r9g":      6,
	"r9gd":     6,
	"t8i":      6,
	"x8aedz":   6,
	"x8i":      6,

	// --- Nitro v5 ---
	"c7gn":      5,
	"c8g":       5,
	"c8gd":      5,
	"hpc7g":     5,
	"i7ie":      5,
	"i8g":       5,
	"m8g":       5,
	"m8gd":      5,
	"mac-m4":    5,
	"mac-m4pro": 5,
	"p5en":      5,
	"p6e-gb200": 5,
	"r8g":       5,
	"r8gd":      5,
	"trn2":      5,
	"trn2u":     5,
	"x8g":       5,

	// --- Nitro v4 ---
	"c6a":        4,
	"c6gn":       4,
	"c6i":        4,
	"c6id":       4,
	"c6in":       4,
	"c7a":        4,
	"c7g":        4,
	"c7gd":       4,
	"c7i":        4,
	"c7i-flex":   4,
	"f2":         4,
	"g6":         4,
	"g6e":        4,
	"g6f":        4,
	"gr6":        4,
	"gr6f":       4,
	"hpc6a":      4,
	"hpc6id":     4,
	"hpc7a":      4,
	"i4g":        4,
	"i4i":        4,
	"i7i":        4,
	"im4gn":      4,
	"inf2":       4,
	"is4gen":     4,
	"m6a":        4,
	"m6i":        4,
	"m6id":       4,
	"m6idn":      4,
	"m6in":       4,
	"m7a":        4,
	"m7g":        4,
	"m7gd":       4,
	"m7i":        4,
	"m7i-flex":   4,
	"p5":         4,
	"p5e":        4,
	"r6a":        4,
	"r6i":        4,
	"r6id":       4,
	"r6idn":      4,
	"r6in":       4,
	"r7a":        4,
	"r7g":        4,
	"r7gd":       4,
	"r7i":        4,
	"r7iz":       4,
	"trn1":       4,
	"trn1n":      4,
	"u7i-12tb":   4,
	"u7i-6tb":    4,
	"u7i-8tb":    4,
	"u7in-16tb":  4,
	"u7in-24tb":  4,
	"u7in-32tb":  4,
	"u7inh-32tb": 4,
	"x2idn":      4,
	"x2iedn":     4,

	// --- Nitro v3 ---
	"c5n":     3,
	"d3":      3,
	"d3en":    3,
	"dl1":     3,
	"dl2q":    3,
	"g4ad":    3,
	"g4dn":    3,
	"g5":      3,
	"i3en":    3,
	"inf1":    3,
	"m5dn":    3,
	"m5n":     3,
	"m5zn":    3,
	"p3dn":    3,
	"p4d":     3,
	"p4de":    3,
	"r5dn":    3,
	"r5n":     3,
	"u-12tb1": 3,
	"u-18tb1": 3,
	"u-24tb1": 3,
	"u-3tb1":  3,
	"u-6tb1":  3,
	"u-9tb1":  3,
	"vt1":     3,
	"x2iezn":  3,

	// --- Nitro v2 ---
	"a1":           2,
	"c5":           2,
	"c5a":          2,
	"c5ad":         2,
	"c5d":          2,
	"c6g":          2,
	"c6gd":         2,
	"g5g":          2,
	"m5":           2,
	"m5a":          2,
	"m5ad":         2,
	"m5d":          2,
	"m6g":          2,
	"m6gd":         2,
	"mac-m3ultra":  2,
	"mac-m4max":    2,
	"mac1":         2,
	"mac2":         2,
	"mac2-m1ultra": 2,
	"mac2-m2":      2,
	"mac2-m2pro":   2,
	"r5":           2,
	"r5a":          2,
	"r5ad":         2,
	"r5b":          2,
	"r5d":          2,
	"r6g":          2,
	"r6gd":         2,
	"t3":           2,
	"t3a":          2,
	"t4g":          2,
	"x2gd":         2,
	"z1d":          2}

// InstanceFamily returns the family portion of an instance type — everything
// before the first dot. EC2 type names are "<family>.<size>", which holds for
// the awkward ones too: m7i-flex.large, p6-b200.48xlarge, u7in-16tb.224xlarge,
// mac2-m2pro.metal.
func InstanceFamily(instanceType string) string {
	t := strings.ToLower(strings.TrimSpace(instanceType))
	if i := strings.Index(t, "."); i >= 0 {
		return t[:i]
	}
	return t
}

// NitroGeneration returns the Nitro System version for an instance type, or
// NitroGenerationUnknown if this table does not classify its family.
//
// Accepts a full type ("c8g.4xlarge") or a bare family ("c8g").
func NitroGeneration(instanceType string) int {
	return nitroGenerationByFamily[InstanceFamily(instanceType)]
}

// NitroFamilies returns every classified family. For the census gates.
func NitroFamilies() map[string]int {
	out := make(map[string]int, len(nitroGenerationByFamily))
	for k, v := range nitroGenerationByFamily {
		out[k] = v
	}
	return out
}

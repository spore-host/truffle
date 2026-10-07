package aws

import (
	"regexp"
	"strings"
	"testing"
)

// TestInstanceFamilyHandlesTheAwkwardNames: EC2 type names are
// "<family>.<size>", but the families themselves contain hyphens and digits in
// patterns that tempt a cleverer parser. A regex over the family part would get
// these wrong; splitting on the first dot does not.
func TestInstanceFamilyHandlesTheAwkwardNames(t *testing.T) {
	cases := map[string]string{
		"c8g.4xlarge":          "c8g",
		"m7i-flex.large":       "m7i-flex",
		"p6-b200.48xlarge":     "p6-b200",
		"p6e-gb200.36xlarge":   "p6e-gb200",
		"u7in-16tb.224xlarge":  "u7in-16tb",
		"u7inh-32tb.480xlarge": "u7inh-32tb",
		"mac2-m2pro.metal":     "mac2-m2pro",
		"hpc7g.16xlarge":       "hpc7g",
		"C8G.4XLARGE":          "c8g", // case-insensitive
		"  c8g.4xlarge  ":      "c8g", // trimmed
		"c8g":                  "c8g", // bare family
	}
	for in, want := range cases {
		if got := InstanceFamily(in); got != want {
			t.Errorf("InstanceFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNitroGenerationUnknownIsNotAGuess is the #175 lesson applied here.
//
// An unclassified family must return NitroGenerationUnknown, never a plausible
// default. A caller that cannot distinguish "we do not know" from "it is v2"
// would make exactly the mistake that made the static price table unsafe for
// spend caps: nothing downstream can tell a real value from a substituted one.
func TestNitroGenerationUnknownIsNotAGuess(t *testing.T) {
	for _, typ := range []string{
		"t2.micro",     // genuinely Xen, not Nitro — must not be given a version
		"m4.large",     // Xen
		"zz9.plural",   // does not exist
		"",             // empty
		"c99g.1xlarge", // plausible-looking future family we have not classified
	} {
		if got := NitroGeneration(typ); got != NitroGenerationUnknown {
			t.Errorf("NitroGeneration(%q) = %d, want unknown (%d) — a guessed version is "+
				"indistinguishable from a real one downstream",
				typ, got, NitroGenerationUnknown)
		}
	}
}

// TestNitroTableIsWellFormed: structural properties that would make the table
// quietly wrong rather than visibly incomplete.
func TestNitroTableIsWellFormed(t *testing.T) {
	fams := NitroFamilies()
	if len(fams) < 150 {
		t.Fatalf("only %d families classified; the table looks truncated and every gate "+
			"built on it would pass vacuously", len(fams))
	}

	famRE := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	for fam, gen := range fams {
		// A family key with a dot means a full instance TYPE was pasted in, which
		// would never match a lookup — InstanceFamily strips at the dot.
		if strings.Contains(fam, ".") {
			t.Errorf("family key %q contains a dot; keys must be families, not types", fam)
		}
		if !famRE.MatchString(fam) {
			t.Errorf("family key %q is not a plausible EC2 family name", fam)
		}
		// The AWS page's lowest listed generation is v2 and its highest is v6.
		// A 0 here would collide with NitroGenerationUnknown and read as absent.
		if gen < 2 || gen > 6 {
			t.Errorf("family %q has generation %d, outside the v2–v6 the source documents "+
				"(and 0 would be indistinguishable from unclassified)", fam, gen)
		}
	}
}

// TestNitroTableRecordsItsProvenance: the staleness of a hand-maintained table
// must be a fact in the file, not a guess by whoever reads it next.
func TestNitroTableRecordsItsProvenance(t *testing.T) {
	if !strings.HasPrefix(NitroGenerationSource, "https://docs.aws.amazon.com/") {
		t.Errorf("NitroGenerationSource = %q, want the AWS docs URL the data came from",
			NitroGenerationSource)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(NitroGenerationAsOf) {
		t.Errorf("NitroGenerationAsOf = %q, want YYYY-MM-DD", NitroGenerationAsOf)
	}
}

// TestKnownGenerationBoundaries pins a few entries that are easy to get wrong
// and that a careless bulk edit would flatten. c7gn being v5 while c7g is v4 is
// the one most likely to be "corrected" to the wrong thing.
func TestKnownGenerationBoundaries(t *testing.T) {
	cases := map[string]int{
		"c7g.16xlarge":   4,
		"c7gn.16xlarge":  5, // NOT v4, despite the c7g sibling
		"c8g.4xlarge":    5,
		"c9g.4xlarge":    6,
		"m8g.4xlarge":    5,
		"m8i.4xlarge":    6, // Intel 8th gen is v6 while Graviton m8g is v5
		"hpc7g.16xlarge": 5,
		"hpc7a.96xlarge": 4,
		"p5.48xlarge":    4,
		"p5en.48xlarge":  5,
		"c5.large":       2,
		"c5n.18xlarge":   3,
	}
	for typ, want := range cases {
		if got := NitroGeneration(typ); got != want {
			t.Errorf("NitroGeneration(%q) = v%d, want v%d", typ, got, want)
		}
	}
}

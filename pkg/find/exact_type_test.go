package find

import (
	"sort"
	"testing"

	libpricing "github.com/spore-host/libs/pricing"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/metadata"
)

// knownInstanceTypes discovers every instance type truffle carries in embedded
// data: the static price table (libs/pricing, every region) and the GPU
// catalog's exact instance types (pkg/metadata), plus every known family
// (pkg/metadata) with a size. It is discovered rather than
// listed so that a type added to either source is covered without touching
// this test (spawn CLAUDE.md gate rule 4).
func knownInstanceTypes(t *testing.T) []string {
	t.Helper()
	set := make(map[string]bool)
	for _, byType := range libpricing.EC2Pricing {
		for it := range byType {
			set[it] = true
		}
	}
	fromPricing := len(set)
	for _, info := range metadata.GPUDatabase {
		for _, it := range info.InstanceTypes {
			set[it] = true
		}
	}
	// Every known family, with a size, so the hermetic set also holds the
	// family shapes the price table and GPU catalog lack ("u-6tb1.112xlarge",
	// "mac-m4.metal": a hyphenated word before the generation digit).
	families := metadata.InstanceFamilies()
	for _, f := range families {
		set[f+".metal"] = true
	}
	// A discovery that finds nothing has broken, not proven the parser right.
	if fromPricing == 0 || len(families) == 0 || len(set) == fromPricing {
		t.Fatalf("instance-type discovery is broken: %d types from libs/pricing, %d families, %d types in all",
			fromPricing, len(families), len(set))
	}
	out := make([]string, 0, len(set))
	for it := range set {
		out = append(out, it)
	}
	sort.Strings(out)
	return out
}

// TestParseQuery_ExactInstanceTypeResolvesToItself is the #263 gate. An exact
// instance type is the most specific query there is, yet the library's
// ParseQuery reported "r8g.2xlarge" as an unknown term: `truffle find` only
// worked because cmd checked for a pattern before calling ParseQuery, and
// library callers (spore-host-mcp's truffle_find) got the refusal.
//
// Every known instance type, queried verbatim through the library entry point
// (ParseQuery, then BuildCriteria), must parse, and its pattern must match that
// type and no other known type.
func TestParseQuery_ExactInstanceTypeResolvesToItself(t *testing.T) {
	all := knownInstanceTypes(t)
	t.Logf("checking %d instance types", len(all))
	for _, it := range all {
		pq, err := ParseQuery(it)
		if err != nil {
			t.Errorf("ParseQuery(%q) error: %v", it, err)
			continue
		}
		crit, err := pq.BuildCriteria(false)
		if err != nil {
			t.Errorf("ParseQuery(%q).BuildCriteria error: %v", it, err)
			continue
		}
		re := crit.InstanceTypePattern
		var matched []string
		for _, other := range all {
			if re.MatchString(other) {
				matched = append(matched, other)
			}
		}
		if len(matched) != 1 || matched[0] != it {
			t.Errorf("query %q: pattern %s matches %v, want exactly [%s]", it, re, matched, it)
		}
		if crit.FilterOptions != (aws.FilterOptions{}) {
			t.Errorf("query %q: FilterOptions = %+v, want none (an exact type needs no other filter)", it, crit.FilterOptions)
		}
	}
}

// TestParseQuery_PatternQueries: globs, regexes and bare families go through
// ParseQuery too, matching what `truffle find` matched for them, and an
// invalid regex is an error rather than a panic in BuildCriteria.
func TestParseQuery_PatternQueries(t *testing.T) {
	for _, tt := range []struct {
		query   string
		match   []string
		noMatch []string
	}{
		{"m7i.*", []string{"m7i.large", "m7i.48xlarge"}, []string{"m7a.large", "m6i.large"}},
		{"g5", []string{"g5.xlarge", "g5.48xlarge"}, []string{"g5g.xlarge", "g6.xlarge"}},
		{"c[6-7]i.large", []string{"c6i.large", "c7i.large"}, []string{"c8i.large", "c6i.xlarge"}},
		{"  r8g.2xlarge  ", []string{"r8g.2xlarge"}, []string{"r8g.4xlarge", "r8gd.2xlarge"}},
	} {
		pq, err := ParseQuery(tt.query)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", tt.query, err)
		}
		if !pq.IsPattern() {
			t.Fatalf("ParseQuery(%q).IsPattern() = false", tt.query)
		}
		crit, err := pq.BuildCriteria(true)
		if err != nil {
			t.Fatalf("BuildCriteria(%q): %v", tt.query, err)
		}
		if !crit.FilterOptions.IncludeAZs {
			t.Errorf("%q: includeAZs not passed through", tt.query)
		}
		for _, m := range tt.match {
			if !crit.InstanceTypePattern.MatchString(m) {
				t.Errorf("%q: pattern %s does not match %s", tt.query, crit.InstanceTypePattern, m)
			}
		}
		for _, m := range tt.noMatch {
			if crit.InstanceTypePattern.MatchString(m) {
				t.Errorf("%q: pattern %s matches %s", tt.query, crit.InstanceTypePattern, m)
			}
		}
	}

	if _, err := ParseQuery("c6[i"); err == nil {
		t.Error(`ParseQuery("c6[i") = nil error, want an invalid-pattern error`)
	}

	// Natural-language queries are untouched: still tokenised, not patterns.
	for _, q := range []string{"r8g 2xlarge", "h100", "B200+", "a100-80gb", "8 cores 32gb"} {
		pq, err := ParseQuery(q)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", q, err)
		}
		if pq.IsPattern() {
			t.Errorf("ParseQuery(%q) read as a pattern; want the natural-language parse", q)
		}
	}
	var nilPQ *ParsedQuery
	if nilPQ.IsPattern() {
		t.Error("nil ParsedQuery reports IsPattern")
	}
}

// TestParseQuery_BareFamilyMatchesOnlyThatFamily: every known family, queried
// alone through the library entry point, matches that family's types and no
// other family's (#263, #229).
func TestParseQuery_BareFamilyMatchesOnlyThatFamily(t *testing.T) {
	families := metadata.InstanceFamilies()
	if len(families) == 0 {
		t.Fatal("no families discovered")
	}
	for _, f := range families {
		if IsRecognizedTerm(f) {
			// A family that is also query vocabulary ("trn1", the Trainium
			// accelerator) means the accelerator, whose types span families
			// (trn1 and trn1n); that is the natural-language path, by design.
			continue
		}
		pq, err := ParseQuery(f)
		if err != nil {
			t.Errorf("ParseQuery(%q): %v", f, err)
			continue
		}
		crit, err := pq.BuildCriteria(false)
		if err != nil {
			t.Errorf("BuildCriteria(%q): %v", f, err)
			continue
		}
		for _, g := range families {
			if got, want := crit.InstanceTypePattern.MatchString(g+".xlarge"), g == f; got != want {
				t.Errorf("query %q: pattern %s matching %s.xlarge = %v, want %v", f, crit.InstanceTypePattern, g, got, want)
			}
		}
	}
}

package aws_test

import (
	"slices"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
)

// TestEFAQueries_FixtureMetadata pins EFA queries end to end, from
// find.ParseQuery through BuildCriteria to the per-region matching, over
// fixture DescribeInstanceTypes records whose NetworkInfo.EfaSupported is as
// EC2 reported it (#253).
//
// "h200 efa" matched nothing: the EFA family list stopped at p5, so the EFA
// dimension intersected H200's families (p5e, p5en) down to nothing. EFA is
// now EC2's per-type flag, so a type is in when its record says so,
// whatever family it belongs to, and out when it doesn't, even in a family
// that has EFA at other sizes (c6a.large vs c6a.48xlarge).
func TestEFAQueries_FixtureMetadata(t *testing.T) {
	efa := aws.FixtureEFATypes()
	if len(efa) == 0 {
		t.Fatal("fixture has no EFA-capable types; the test would pass vacuously")
	}
	h100 := []string{"p5.4xlarge", "p5.48xlarge"}
	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"h200 efa", []string{"p5e.48xlarge", "p5en.48xlarge"}},
		{"h100 efa", h100},
		{"efa", efa},
		{"b200 efa", []string{"p6-b200.48xlarge"}},
		{"h100 or efa", union(h100, efa)},
		{"efa 128 cores", []string{"c6a.48xlarge", "p5.48xlarge", "p5e.48xlarge", "p5en.48xlarge", "p6-b200.48xlarge", "u7i-6tb.112xlarge"}},
	} {
		pq, err := find.ParseQuery(tt.query)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", tt.query, err)
		}
		crit, err := pq.BuildCriteria(false)
		if err != nil {
			t.Fatalf("BuildCriteria(%q): %v", tt.query, err)
		}
		got, err := aws.SearchFixture(crit.InstanceTypePattern, crit.FilterOptions)
		if err != nil {
			t.Fatalf("%q: %v", tt.query, err)
		}
		slices.Sort(got)
		want := slices.Clone(tt.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%q (pattern %s) = %v, want %v", tt.query, crit.InstanceTypePattern, got, want)
		}
	}
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

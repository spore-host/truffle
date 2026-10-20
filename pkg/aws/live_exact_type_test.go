package aws_test

import (
	"sort"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
)

// TestLiveExactInstanceTypesResolveToThemselves is the live counterpart of
// find's TestParseQuery_ExactInstanceTypeResolvesToItself (#263): every type
// EC2 offers, queried verbatim through find.ParseQuery and BuildCriteria,
// matches that type and no other offered type. The embedded data the hermetic
// gate discovers covers a few hundred types; this covers the whole catalog.
// Skips unless TRUFFLE_LIVE_AWS=1; it only reads.
func TestLiveExactInstanceTypesResolveToThemselves(t *testing.T) {
	set := map[string]bool{}
	for _, it := range aws.LiveCatalog(t) {
		set[string(it.InstanceType)] = true
	}
	all := make([]string, 0, len(set))
	for it := range set {
		all = append(all, it)
	}
	sort.Strings(all)
	t.Logf("checking %d live instance types", len(all))

	for _, it := range all {
		pq, err := find.ParseQuery(it)
		if err != nil {
			t.Errorf("ParseQuery(%q): %v", it, err)
			continue
		}
		crit, err := pq.BuildCriteria(false)
		if err != nil {
			t.Errorf("BuildCriteria(%q): %v", it, err)
			continue
		}
		var matched []string
		for _, other := range all {
			if crit.InstanceTypePattern.MatchString(other) {
				matched = append(matched, other)
			}
		}
		if len(matched) != 1 || matched[0] != it {
			t.Errorf("query %q: pattern %s matches %v, want exactly [%s]", it, crit.InstanceTypePattern, matched, it)
		}
	}
}

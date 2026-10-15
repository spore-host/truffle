package aws

import (
	"context"
	"math/rand"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/testutil"
)

// TestAZsByInstanceType_OrderIndependent is the gate for #217:
// DescribeInstanceTypeOfferings returns offerings in no particular order, and
// each type's AZ list must come out the same (sorted, deduplicated) however
// they arrive.
func TestAZsByInstanceType_OrderIndependent(t *testing.T) {
	loc := func(s string) *string { return &s }
	var offerings []types.InstanceTypeOffering
	for _, az := range []string{"us-west-2a", "us-west-2b", "us-west-2c", "us-west-2d", "us-west-2-lax-1a"} {
		offerings = append(offerings, types.InstanceTypeOffering{InstanceType: "m8a.4xlarge", Location: loc(az)})
	}
	for _, az := range []string{"us-west-2b", "us-west-2c"} {
		offerings = append(offerings, types.InstanceTypeOffering{InstanceType: "c8a.large", Location: loc(az)})
	}
	// A duplicate row and a nil location must not show up in the list.
	offerings = append(offerings,
		types.InstanceTypeOffering{InstanceType: "c8a.large", Location: loc("us-west-2b")},
		types.InstanceTypeOffering{InstanceType: "c8a.large"},
	)

	want := map[string][]string{
		"m8a.4xlarge": {"us-west-2a", "us-west-2b", "us-west-2c", "us-west-2d", "us-west-2-lax-1a"},
		"c8a.large":   {"us-west-2b", "us-west-2c"},
	}
	rng := rand.New(rand.NewSource(217))
	for i := 0; i < 50; i++ {
		shuffled := append([]types.InstanceTypeOffering(nil), offerings...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := azsByInstanceType(shuffled); !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d: azsByInstanceType = %v, want %v", i, got, want)
		}
	}
}

// TestAZLookupsAreSorted checks both AZ lookups end to end against the
// emulator: the per-type one (used by `available`) and the batched one (used
// by find/search/az) must both return sorted lists (#217).
func TestAZLookupsAreSorted(t *testing.T) {
	env := testutil.SubstrateServer(t)
	ctx := context.Background()
	c := NewClientFromConfig(env.AWSConfig)

	names, err := c.GetInstanceTypes(ctx, "us-east-1")
	if err != nil || len(names) == 0 {
		t.Fatalf("GetInstanceTypes: %v (%d types)", err, len(names))
	}
	batch := c.getAvailabilityZonesBatch(ctx, "us-east-1", names)
	if len(batch) == 0 {
		t.Fatal("batched lookup returned no AZs")
	}
	for typ, azs := range batch {
		if !ZoneNamesSorted(azs) {
			t.Errorf("batched AZs for %s not sorted: %v", typ, azs)
		}
		single, err := c.getAvailabilityZones(ctx, "us-east-1", typ)
		if err != nil {
			t.Fatalf("getAvailabilityZones(%s): %v", typ, err)
		}
		if !reflect.DeepEqual(single, azs) {
			t.Errorf("%s: per-type AZs %v, batched %v", typ, single, azs)
		}
	}
}

// TestSortZoneNames: standard AZs first, then Local and Wavelength Zones, each
// group alphabetically, so the truncated table column leads with in-region
// AZs (#217).
func TestSortZoneNames(t *testing.T) {
	want := []string{
		"us-east-1a", "us-east-1b", "us-east-1f",
		"us-east-1-bos-1a", "us-east-1-mia-2a",
		"us-east-1-wl1-bos-wlz-1",
	}
	rng := rand.New(rand.NewSource(217))
	for i := 0; i < 50; i++ {
		got := append([]string(nil), want...)
		rng.Shuffle(len(got), func(a, b int) { got[a], got[b] = got[b], got[a] })
		SortZoneNames(got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d: SortZoneNames = %v, want %v", i, got, want)
		}
	}
	if !ZoneNamesSorted(want) {
		t.Errorf("ZoneNamesSorted(%v) = false", want)
	}
	if ZoneNamesSorted([]string{"us-east-1-bos-1a", "us-east-1a"}) {
		t.Error("ZoneNamesSorted accepted an edge zone ahead of a standard AZ")
	}
}

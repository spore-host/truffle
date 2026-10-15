package cmd

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// shuffledRuns calls check with n differently shuffled copies of in.
func shuffledRuns[T any](in []T, n int, check func(run int, s []T)) {
	rng := rand.New(rand.NewSource(217))
	for i := 0; i < n; i++ {
		s := append([]T(nil), in...)
		rng.Shuffle(len(s), func(a, b int) { s[a], s[b] = s[b], s[a] })
		check(i, s)
	}
}

// TestSortSpotResults_DeterministicOnPriceTies: one spot price across several
// AZs is common, so --sort-by-price must still order the tied rows the same
// way every run (#217).
func TestSortSpotResults_DeterministicOnPriceTies(t *testing.T) {
	want := []aws.SpotPriceResult{
		{InstanceType: "m8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2a", SpotPrice: 0.05},
		{InstanceType: "m8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2b", SpotPrice: 0.05},
		{InstanceType: "m8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2c", SpotPrice: 0.05},
		{InstanceType: "m8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2d", SpotPrice: 0.06},
	}
	shuffledRuns(want, 50, func(run int, s []aws.SpotPriceResult) {
		sortSpotResults(s, true)
		if !reflect.DeepEqual(s, want) {
			t.Fatalf("run %d: by price got %v, want %v", run, s, want)
		}
	})

	byKey := []aws.SpotPriceResult{
		{InstanceType: "c8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2b", SpotPrice: 0.09},
		{InstanceType: "m8a.large", Region: "us-east-1", AvailabilityZone: "us-east-1a", SpotPrice: 0.01},
		{InstanceType: "m8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2a", SpotPrice: 0.05},
		{InstanceType: "m8a.large", Region: "us-west-2", AvailabilityZone: "us-west-2c", SpotPrice: 0.02},
	}
	shuffledRuns(byKey, 50, func(run int, s []aws.SpotPriceResult) {
		sortSpotResults(s, false)
		if !reflect.DeepEqual(s, byKey) {
			t.Fatalf("run %d: default order got %v, want %v", run, s, byKey)
		}
	})
}

// TestSortCapacity_DeterministicAcrossAZs: reservations and blocks of the same
// type in several AZs must not reorder between runs (#217).
func TestSortCapacity_DeterministicAcrossAZs(t *testing.T) {
	res := []aws.CapacityReservationResult{
		{ReservationID: "cr-2", InstanceType: "p5.48xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1a", AvailableCapacity: 2},
		{ReservationID: "cr-1", InstanceType: "p5.48xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1b", AvailableCapacity: 2},
		{ReservationID: "cr-3", InstanceType: "p5.48xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1b", AvailableCapacity: 2},
		{ReservationID: "cr-0", InstanceType: "p5.48xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1a", AvailableCapacity: 1},
	}
	shuffledRuns(res, 50, func(run int, s []aws.CapacityReservationResult) {
		sortCapacityReservations(s)
		if !reflect.DeepEqual(s, res) {
			t.Fatalf("run %d: reservations got %v, want %v", run, s, res)
		}
	})

	blocks := []aws.CapacityBlockResult{
		{CapacityBlockID: "cb-2", InstanceType: "p5.48xlarge", AvailabilityZone: "us-east-1a", StartDate: "2026-10-20"},
		{CapacityBlockID: "cb-1", InstanceType: "p5.48xlarge", AvailabilityZone: "us-east-1b", StartDate: "2026-10-20"},
		{CapacityBlockID: "cb-3", InstanceType: "p5.48xlarge", AvailabilityZone: "us-east-1b", StartDate: "2026-10-20"},
		{CapacityBlockID: "cb-0", InstanceType: "p5.48xlarge", AvailabilityZone: "us-east-1a", StartDate: "2026-10-21"},
	}
	shuffledRuns(blocks, 50, func(run int, s []aws.CapacityBlockResult) {
		sortCapacityBlocks(s)
		if !reflect.DeepEqual(s, blocks) {
			t.Fatalf("run %d: blocks got %v, want %v", run, s, blocks)
		}
	})
}

// TestSummarizeAZRegions_ZoneOrder: `az --regions-only` lists standard AZs
// before Local Zones, like every other AZ list (#217).
func TestSummarizeAZRegions_ZoneOrder(t *testing.T) {
	got := summarizeAZRegions([]aws.InstanceTypeResult{
		{InstanceType: "g4dn.xlarge", Region: "us-west-2", AvailableAZs: []string{"us-west-2-lax-1a", "us-west-2b"}},
		{InstanceType: "g5.xlarge", Region: "us-west-2", AvailableAZs: []string{"us-west-2a"}},
	})
	want := []string{"us-west-2a", "us-west-2b", "us-west-2-lax-1a"}
	if len(got) != 1 || !reflect.DeepEqual(got[0].AvailabilityZones, want) {
		t.Fatalf("summarizeAZRegions AZs = %+v, want %v", got, want)
	}
}

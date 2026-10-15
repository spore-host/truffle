package cmd

import (
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
)

// turinResults is the shape of `truffle find "amd turin 16 cores 64gb"` from
// truffle#187, deliberately fed in the old largest-first order.
func turinResults() []aws.InstanceTypeResult {
	mk := func(it, region string, vcpu int32, memGiB int64, price float64) aws.InstanceTypeResult {
		return aws.InstanceTypeResult{InstanceType: it, Region: region, VCPUs: vcpu, MemoryMiB: memGiB * 1024, Architecture: "x86_64", OnDemandPrice: price}
	}
	return []aws.InstanceTypeResult{
		mk("r8a.48xlarge", "us-west-2", 192, 1536, 15.337),
		mk("m8a.48xlarge", "us-west-2", 192, 768, 11.6851),
		mk("m8a.8xlarge", "us-west-2", 32, 128, 1.9475),
		mk("c8a.8xlarge", "us-west-2", 32, 64, 1.7243),
		mk("m8a.4xlarge", "us-east-1", 16, 64, 1.0100),
		mk("r8a.4xlarge", "us-west-2", 16, 128, 1.2781),
		mk("m8a.4xlarge", "us-west-2", 16, 64, 0.9738),
		mk("r8a.48xlarge", "us-east-1", 192, 1536, 15.337),
	}
}

func types(rs []aws.InstanceTypeResult) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.InstanceType+"@"+r.Region)
	}
	return out
}

// TestSortFindResults_ClosestFitFirst asserts the documented intent of
// truffle#187: size terms are minimums, but the type closest to the request
// comes first (not the largest/most expensive), equal-distance types go
// cheapest first, and each type's rows stay together regardless of region
// with the cheapest region first.
func TestSortFindResults_ClosestFitFirst(t *testing.T) {
	rs := turinResults()
	sortFindResults(rs, aws.FilterOptions{MinVCPUs: 16, MinMemory: 64}, find.SortDefault)

	want := []string{
		"m8a.4xlarge@us-west-2", // exact match, cheaper region first
		"m8a.4xlarge@us-east-1",
		"r8a.4xlarge@us-west-2", // distance 1 (2x memory), $1.28
		"c8a.8xlarge@us-west-2", // distance 1 (2x vCPU), $1.72
		"m8a.8xlarge@us-west-2", // distance 2
		"m8a.48xlarge@us-west-2",
		"r8a.48xlarge@us-east-1", // equal price: region name tiebreak
		"r8a.48xlarge@us-west-2",
	}
	got := types(rs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestSortFindResults_NoSizeSpecNewestFirst keeps the pre-#187 default for
// queries without size terms: newest generation first.
func TestSortFindResults_NoSizeSpecNewestFirst(t *testing.T) {
	rs := []aws.InstanceTypeResult{
		{InstanceType: "m6a.large", Region: "us-west-2", VCPUs: 2, MemoryMiB: 8192, OnDemandPrice: 0.08},
		{InstanceType: "m8a.large", Region: "us-west-2", VCPUs: 2, MemoryMiB: 8192, OnDemandPrice: 0.12},
	}
	sortFindResults(rs, aws.FilterOptions{}, find.SortDefault)
	if rs[0].InstanceType != "m8a.large" {
		t.Errorf("first = %s, want m8a.large (newest generation)", rs[0].InstanceType)
	}
}

// TestSortFindResults_QualitativeOverrides checks that "cheap"/"expensive"
// still decide the primary key, ranked per type.
func TestSortFindResults_QualitativeOverrides(t *testing.T) {
	f := aws.FilterOptions{MinVCPUs: 16, MinMemory: 64}

	rs := turinResults()
	sortFindResults(rs, f, find.SortExpensive)
	if rs[0].InstanceType != "r8a.48xlarge" {
		t.Errorf("expensive: first = %s, want r8a.48xlarge", rs[0].InstanceType)
	}

	rs = turinResults()
	sortFindResults(rs, f, find.SortCheapest)
	if got := types(rs)[:2]; got[0] != "m8a.4xlarge@us-west-2" || got[1] != "m8a.4xlarge@us-east-1" {
		t.Errorf("cheapest: first rows = %v, want m8a.4xlarge us-west-2 then us-east-1", got)
	}
}

func TestFitDistance(t *testing.T) {
	f := aws.FilterOptions{MinVCPUs: 16, MinMemory: 64}
	exact := aws.InstanceTypeResult{VCPUs: 16, MemoryMiB: 64 * 1024}
	moreMem := aws.InstanceTypeResult{VCPUs: 16, MemoryMiB: 128 * 1024}
	moreCPU := aws.InstanceTypeResult{VCPUs: 32, MemoryMiB: 64 * 1024}
	huge := aws.InstanceTypeResult{VCPUs: 192, MemoryMiB: 1536 * 1024}

	if d := fitDistance(exact, f); d != 0 {
		t.Errorf("exact match distance = %v, want 0", d)
	}
	if a, b := fitDistance(moreMem, f), fitDistance(moreCPU, f); a != 1 || b != 1 {
		t.Errorf("doubling one dimension: got %v and %v, want 1 and 1", a, b)
	}
	if d := fitDistance(huge, f); d <= fitDistance(moreCPU, f) {
		t.Errorf("192/1536 distance %v should exceed 32/64's", d)
	}
	if d := fitDistance(huge, aws.FilterOptions{}); d != 0 {
		t.Errorf("no size spec: distance = %v, want 0", d)
	}
}

func TestMinimumSpecHint(t *testing.T) {
	rs := turinResults()
	got := minimumSpecHint(rs, aws.FilterOptions{MinVCPUs: 16, MinMemory: 64}, find.SortDefault)
	want := "Showing 6 types with ≥16 vCPU and ≥64 GiB memory, closest fit first; use --exact for an exact match"
	if got != want {
		t.Errorf("hint =\n %q\nwant\n %q", got, want)
	}
	if got := minimumSpecHint(rs, aws.FilterOptions{MinMemory: 0.5}, find.SortCheapest); got != "Showing 6 types with ≥0.5 GiB memory; use --exact for an exact match" {
		t.Errorf("non-default sort hint = %q", got)
	}
	if got := minimumSpecHint(rs, aws.FilterOptions{MinVCPUs: 16, ExactVCPUs: true}, find.SortDefault); got != "" {
		t.Errorf("--exact should suppress the hint, got %q", got)
	}
	if got := minimumSpecHint(rs, aws.FilterOptions{}, find.SortDefault); got != "" {
		t.Errorf("no size terms should suppress the hint, got %q", got)
	}
}

// TestSortFindResults_TurinDemoClosestFitFirst: with x8aedz in the Turin
// families (#228), the demo query `find "amd turin 16 cores 64gb"` still
// leads with the exact fit; x8aedz.6xlarge (24 vCPU, 768 GiB, far from 64 GiB)
// sorts below the close fits. Prices are us-west-2's on 2026-10-15.
func TestSortFindResults_TurinDemoClosestFitFirst(t *testing.T) {
	mk := func(it string, vcpu int32, memGiB int64, price float64) aws.InstanceTypeResult {
		return aws.InstanceTypeResult{InstanceType: it, Region: "us-west-2", VCPUs: vcpu, MemoryMiB: memGiB * 1024, Architecture: "x86_64", OnDemandPrice: price}
	}
	rs := []aws.InstanceTypeResult{
		mk("x8aedz.6xlarge", 24, 768, 6.9036),
		mk("r8a.4xlarge", 16, 128, 1.2781),
		mk("m8a.4xlarge", 16, 64, 0.9738),
		mk("c8a.8xlarge", 32, 64, 1.7243),
		mk("m8azn.6xlarge", 24, 96, 2.4775),
	}
	sortFindResults(rs, aws.FilterOptions{MinVCPUs: 16, MinMemory: 64}, find.SortDefault)
	got := types(rs)
	if got[0] != "m8a.4xlarge@us-west-2" || got[len(got)-1] != "x8aedz.6xlarge@us-west-2" {
		t.Errorf("order = %v, want m8a.4xlarge first and x8aedz.6xlarge last", got)
	}
}

// TestSortFindResults_GPUMemoryClosestFit checks closest-fit ordering for a
// per-GPU memory request (#236): for "gpu 24gb" the 24 GB A10G and L4 lead
// (cheapest first), then the 32 GB, 48 GB and 80 GB GPUs.
func TestSortFindResults_GPUMemoryClosestFit(t *testing.T) {
	mk := func(it string, accelMiB int64, price float64) aws.InstanceTypeResult {
		return aws.InstanceTypeResult{InstanceType: it, Region: "us-east-1", VCPUs: 4, MemoryMiB: 16384, AccelMemoryMiB: accelMiB, OnDemandPrice: price}
	}
	rs := []aws.InstanceTypeResult{
		mk("p5.4xlarge", 81920, 6.88),  // H100, 80 GB
		mk("g6e.xlarge", 45776, 1.861), // L40S, 48 GB
		mk("g7.2xlarge", 32768, 2.52),  // RTX PRO 4500, 32 GB
		mk("g5.xlarge", 22888, 1.006),  // A10G, 24 GB
		mk("g6.xlarge", 22888, 0.8048), // L4, 24 GB
	}
	f := aws.FilterOptions{MinGPUMemory: 24}
	sortFindResults(rs, f, find.SortDefault)
	want := "g6.xlarge@us-east-1,g5.xlarge@us-east-1,g7.2xlarge@us-east-1,g6e.xlarge@us-east-1,p5.4xlarge@us-east-1"
	if got := strings.Join(types(rs), ","); got != want {
		t.Errorf("order mismatch\n got: %s\nwant: %s", got, want)
	}
	if d := fitDistance(rs[0], f); d != 0 {
		t.Errorf("fitDistance(L4, 24gb) = %v, want 0", d)
	}
	if got := minimumSpecHint(rs, f, find.SortDefault); got != "Showing 5 types with ≥24 GB per GPU, closest fit first; use --exact for an exact match" {
		t.Errorf("hint = %q", got)
	}
	if got := minimumSpecHint(rs, aws.FilterOptions{MinGPUMemory: 24, ExactGPUMemory: true}, find.SortDefault); got != "" {
		t.Errorf("exact hint = %q, want none", got)
	}
}

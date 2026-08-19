package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/testutil"
)

// instanceTypeInfo builds a minimal types.InstanceTypeInfo for filter tests.
func instanceTypeInfo(name string, vcpus int32, memMiB int64, arches ...types.ArchitectureType) types.InstanceTypeInfo {
	return types.InstanceTypeInfo{
		InstanceType: types.InstanceType(name),
		VCpuInfo:     &types.VCpuInfo{DefaultVCpus: aws.Int32(vcpus)},
		MemoryInfo:   &types.MemoryInfo{SizeInMiB: aws.Int64(memMiB)},
		ProcessorInfo: &types.ProcessorInfo{
			SupportedArchitectures: arches,
		},
	}
}

func TestMatchesFilters(t *testing.T) {
	// m6i.2xlarge: 8 vCPU, 32 GiB, x86_64
	it := instanceTypeInfo("m6i.2xlarge", 8, 32*1024, types.ArchitectureTypeX8664)

	tests := []struct {
		name string
		opts FilterOptions
		want bool
	}{
		{"no filters", FilterOptions{}, true},
		{"arch match", FilterOptions{Architecture: "x86_64"}, true},
		{"arch mismatch", FilterOptions{Architecture: "arm64"}, false},
		{"min vcpu pass", FilterOptions{MinVCPUs: 4}, true},
		{"min vcpu equal", FilterOptions{MinVCPUs: 8}, true},
		{"min vcpu fail", FilterOptions{MinVCPUs: 16}, false},
		{"exact vcpu match", FilterOptions{MinVCPUs: 8, ExactVCPUs: true}, true},
		{"exact vcpu mismatch", FilterOptions{MinVCPUs: 4, ExactVCPUs: true}, false},
		{"min mem pass", FilterOptions{MinMemory: 16}, true},
		{"min mem equal", FilterOptions{MinMemory: 32}, true},
		{"min mem fail", FilterOptions{MinMemory: 64}, false},
		{"exact mem match", FilterOptions{MinMemory: 32, ExactMemory: true}, true},
		{"exact mem within tolerance", FilterOptions{MinMemory: 31.995, ExactMemory: true}, true},
		{"exact mem outside tolerance", FilterOptions{MinMemory: 31.7, ExactMemory: true}, false},
		{"exact mem mismatch", FilterOptions{MinMemory: 16, ExactMemory: true}, false},
		{"family match", FilterOptions{InstanceFamily: "m6i"}, true},
		{"family mismatch", FilterOptions{InstanceFamily: "c6i"}, false},
		{"combined pass", FilterOptions{Architecture: "x86_64", MinVCPUs: 4, MinMemory: 16, InstanceFamily: "m6i"}, true},
		{"combined one fails", FilterOptions{Architecture: "x86_64", MinVCPUs: 4, InstanceFamily: "c6i"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesFilters(it, tt.opts); got != tt.want {
				t.Errorf("matchesFilters(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
	}
}

func TestMatchesFilters_MultiArch(t *testing.T) {
	// Some types report multiple architectures; a match on any should pass.
	it := instanceTypeInfo("a1.large", 2, 4*1024, types.ArchitectureTypeArm64, types.ArchitectureTypeX8664)
	if !matchesFilters(it, FilterOptions{Architecture: "arm64"}) {
		t.Error("expected arm64 to match multi-arch instance")
	}
	if matchesFilters(it, FilterOptions{Architecture: "i386"}) {
		t.Error("expected i386 not to match")
	}
}

func TestExtractFamily(t *testing.T) {
	tests := []struct{ in, want string }{
		{"m6i.2xlarge", "m6i"},
		{"p4d.24xlarge", "p4d"},
		{"t3.micro", "t3"},
		{"nodot", "nodot"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := extractFamily(tt.in); got != tt.want {
			t.Errorf("extractFamily(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestValueOrZero(t *testing.T) {
	v := int32(42)
	if got := valueOrZero(&v); got != 42 {
		t.Errorf("valueOrZero(&42) = %d, want 42", got)
	}
	if got := valueOrZero[int32](nil); got != 0 {
		t.Errorf("valueOrZero(nil) = %d, want 0", got)
	}
	var s *string
	if got := valueOrZero(s); got != "" {
		t.Errorf("valueOrZero(nil string) = %q, want empty", got)
	}
}

// --- capacity reservation / block paths ---
//
// Substrate does not implement DescribeCapacityReservations (it answers
// InvalidAction), so every region query against it fails. Before #110,
// GetCapacityReservations and GetCapacityBlocks discarded that failure and
// returned (empty, nil) — these tests used to assert exactly that as "empty
// substrate, no error", which only held because the error was being thrown
// away. They now assert the #63 contract instead: a total failure surfaces as
// an error, matching SearchInstanceTypes/GetCapacityBlockOfferings. See
// TestGetCapacityBlockOfferings_AllRegionsFailed for the same fix applied
// earlier to the offerings path (#109), and client_ineligible_test.go for the
// newUnreachableClient-backed versions of this same contract.

func TestGetCapacityReservations_AllRegionsFailed(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	res, err := c.GetCapacityReservations(context.Background(), []string{"us-east-1"}, CapacityReservationOptions{
		OnlyActive:    true,
		OnlyAvailable: true,
		MinCapacity:   1,
	})
	if err == nil {
		t.Fatal("all-regions failure returned nil error — an unanswered query must not read as 'zero reservations'")
	}
	if len(res) != 0 {
		t.Errorf("expected 0 reservations alongside the error, got %d", len(res))
	}
}

func TestGetCapacityReservations_MultiRegionAllFailed(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	// Multiple regions exercises the concurrent fan-out path.
	res, err := c.GetCapacityReservations(context.Background(),
		[]string{"us-east-1", "us-west-2"},
		CapacityReservationOptions{InstanceTypes: []string{"p4d.24xlarge"}})
	if err == nil {
		t.Fatal("all-regions failure returned nil error, want the #63 contract to surface it")
	}
	if len(res) != 0 {
		t.Errorf("expected 0 reservations alongside the error, got %d", len(res))
	}
}

func TestGetCapacityBlocks_AllRegionsFailed(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	res, err := c.GetCapacityBlocks(context.Background(), []string{"us-east-1"}, CapacityBlockOptions{
		InstanceTypes: []string{"p5.48xlarge"},
		OnlyActive:    true,
		MinDuration:   24,
		MaxDuration:   168,
	})
	if err == nil {
		t.Fatal("all-regions failure returned nil error — an unanswered query must not read as 'zero blocks'")
	}
	if len(res) != 0 {
		t.Errorf("expected 0 blocks alongside the error, got %d", len(res))
	}
}

// TestGetCapacityBlockOfferings_AllRegionsFailed pins the #63 contract on the
// offerings-discovery path (#109): when every region query fails, the failure is
// returned rather than reported as an empty list.
//
// This test was previously TestGetCapacityBlockOfferings_Empty and asserted the
// opposite — that an empty Substrate yields "no offerings without error". That
// assertion was only ever satisfied because the error was being discarded: the
// emulator does not implement DescribeCapacityBlockOfferings and answers 400
// UnknownError, so the test never exercised the parameter plumbing it described.
// It was in fact locking in the bug. "No capacity block offerings" is a plausible
// answer to this query, which is exactly why a failed query must not be allowed to
// produce it.
func TestGetCapacityBlockOfferings_AllRegionsFailed(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	res, err := c.GetCapacityBlockOfferings(context.Background(), []string{"us-east-1"}, CapacityBlockOfferingOptions{
		InstanceType:          "p5.48xlarge",
		InstanceCount:         1,
		CapacityDurationHours: 24,
	})
	if err == nil {
		t.Fatal("all-regions failure returned nil error — an unanswered query must not read as 'none available'")
	}
	if len(res) != 0 {
		t.Errorf("expected 0 offerings alongside the error, got %d", len(res))
	}
}

// TestMatchesFilters_NestedVirt covers the nested-virtualization filter: the
// predicate must include a type advertising the feature and exclude one that
// doesn't, but only when the filter is enabled.
func TestMatchesFilters_NestedVirt(t *testing.T) {
	withNV := instanceTypeInfo("c8i.4xlarge", 16, 32*1024, types.ArchitectureTypeX8664)
	withNV.ProcessorInfo.SupportedFeatures = []types.SupportedAdditionalProcessorFeature{
		types.SupportedAdditionalProcessorFeatureNestedVirtualization,
	}
	withoutNV := instanceTypeInfo("c5.4xlarge", 16, 32*1024, types.ArchitectureTypeX8664)

	tests := []struct {
		name string
		it   types.InstanceTypeInfo
		opts FilterOptions
		want bool
	}{
		{"filter off, supported type", withNV, FilterOptions{}, true},
		{"filter off, unsupported type", withoutNV, FilterOptions{}, true},
		{"filter on, supported type", withNV, FilterOptions{NestedVirt: true}, true},
		{"filter on, unsupported type", withoutNV, FilterOptions{NestedVirt: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesFilters(tt.it, tt.opts); got != tt.want {
				t.Errorf("matchesFilters(NestedVirt=%v) = %v, want %v", tt.opts.NestedVirt, got, tt.want)
			}
		})
	}
}

// TestSupportsNestedVirt checks the helper directly, including a nil
// ProcessorInfo and an unrelated feature (amd-sev-snp).
func TestSupportsNestedVirt(t *testing.T) {
	mk := func(feats ...types.SupportedAdditionalProcessorFeature) types.InstanceTypeInfo {
		return types.InstanceTypeInfo{ProcessorInfo: &types.ProcessorInfo{SupportedFeatures: feats}}
	}
	if supportsNestedVirt(mk(types.SupportedAdditionalProcessorFeatureNestedVirtualization)) != true {
		t.Error("type with nested-virtualization feature should be supported")
	}
	if supportsNestedVirt(mk(types.SupportedAdditionalProcessorFeatureAmdSevSnp)) != false {
		t.Error("amd-sev-snp only should NOT be nested-virt supported")
	}
	if supportsNestedVirt(mk()) != false {
		t.Error("no features should be unsupported")
	}
	if supportsNestedVirt(types.InstanceTypeInfo{}) != false {
		t.Error("nil ProcessorInfo should be unsupported (no panic)")
	}
}

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
		{"exact mem within tolerance", FilterOptions{MinMemory: 31.7, ExactMemory: true}, true},
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

// --- capacity reservation / block paths (substrate returns empty, no error) ---

func TestGetCapacityReservations_Empty(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	res, err := c.GetCapacityReservations(context.Background(), []string{"us-east-1"}, CapacityReservationOptions{
		OnlyActive:    true,
		OnlyAvailable: true,
		MinCapacity:   1,
	})
	if err != nil {
		t.Fatalf("GetCapacityReservations error = %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 reservations from empty substrate, got %d", len(res))
	}
}

func TestGetCapacityReservations_MultiRegion(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	// Multiple regions exercises the concurrent fan-out path.
	res, err := c.GetCapacityReservations(context.Background(),
		[]string{"us-east-1", "us-west-2"},
		CapacityReservationOptions{InstanceTypes: []string{"p4d.24xlarge"}})
	if err != nil {
		t.Fatalf("GetCapacityReservations error = %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 reservations, got %d", len(res))
	}
}

func TestGetCapacityBlocks_Empty(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	res, err := c.GetCapacityBlocks(context.Background(), []string{"us-east-1"}, CapacityBlockOptions{
		InstanceTypes: []string{"p5.48xlarge"},
		OnlyActive:    true,
		MinDuration:   24,
		MaxDuration:   168,
	})
	if err != nil {
		t.Fatalf("GetCapacityBlocks error = %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 blocks from empty substrate, got %d", len(res))
	}
}

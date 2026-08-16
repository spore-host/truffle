package aws

import (
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestBuildResultFromEC2_WholeGPU confirms GPUMemoryPerMiB/GPUPartitionSize/
// LogicalGPUs populate correctly for a whole-GPU type, and GPUMemoryPerMiB is
// the raw per-device figure, not GPUMemoryMiB divided by GPUs.
func TestBuildResultFromEC2_WholeGPU(t *testing.T) {
	it := instanceTypeInfo("g5.12xlarge", 48, 196608, types.ArchitectureTypeX8664)
	it.GpuInfo = &types.GpuInfo{
		Gpus: []types.GpuDeviceInfo{
			{
				Count:            awssdk.Int32(4),
				Name:             awssdk.String("A10G"),
				Manufacturer:     awssdk.String("nvidia"),
				MemoryInfo:       &types.GpuDeviceMemoryInfo{SizeInMiB: awssdk.Int32(24576)},
				GpuPartitionSize: awssdk.Float64(1.0),
				LogicalGpuCount:  awssdk.Int32(4),
			},
		},
		TotalGpuMemoryInMiB: awssdk.Int32(98304),
	}

	result := buildResultFromEC2(it, "g5.12xlarge", "us-east-1")

	if result.GPUs != 4 {
		t.Errorf("GPUs = %d, want 4", result.GPUs)
	}
	if result.GPUMemoryMiB != 98304 {
		t.Errorf("GPUMemoryMiB = %d, want 98304 (24576*4)", result.GPUMemoryMiB)
	}
	if result.GPUMemoryPerMiB != 24576 {
		t.Errorf("GPUMemoryPerMiB = %d, want 24576 (raw per-device, not divided)", result.GPUMemoryPerMiB)
	}
	if result.GPUPartitionSize != 1.0 {
		t.Errorf("GPUPartitionSize = %v, want 1.0", result.GPUPartitionSize)
	}
	if result.LogicalGPUs != 4 {
		t.Errorf("LogicalGPUs = %d, want 4", result.LogicalGPUs)
	}
}

// TestBuildResultFromEC2_FractionalGPU is the regression test for #116: a
// fractional-GPU type (e.g. g6f.large) reports Count==0 because AWS
// represents the slice via GpuPartitionSize, not a whole-GPU count. Naively
// computing per-GPU memory as GPUMemoryMiB/GPUs would divide by zero here;
// GPUMemoryPerMiB must come from the raw device field instead, so it must be
// correct and non-zero without ever touching GPUs as a divisor.
func TestBuildResultFromEC2_FractionalGPU(t *testing.T) {
	it := instanceTypeInfo("g6f.large", 8, 16384, types.ArchitectureTypeX8664)
	it.GpuInfo = &types.GpuInfo{
		Gpus: []types.GpuDeviceInfo{
			{
				Count:            awssdk.Int32(0), // AWS convention for a fractional slice
				Name:             awssdk.String("L4"),
				Manufacturer:     awssdk.String("nvidia"),
				MemoryInfo:       &types.GpuDeviceMemoryInfo{SizeInMiB: awssdk.Int32(2861)},
				GpuPartitionSize: awssdk.Float64(0.125),
				LogicalGpuCount:  awssdk.Int32(1),
			},
		},
		TotalGpuMemoryInMiB: awssdk.Int32(2861),
	}

	// Must not panic (no div-by-zero anywhere in the call path).
	result := buildResultFromEC2(it, "g6f.large", "us-east-1")

	if result.GPUs != 0 {
		t.Errorf("GPUs = %d, want 0 (fractional GPU reports Count==0)", result.GPUs)
	}
	if result.GPUMemoryPerMiB != 2861 {
		t.Errorf("GPUMemoryPerMiB = %d, want 2861 (raw per-device figure, recoverable even though GPUs==0)", result.GPUMemoryPerMiB)
	}
	if result.GPUPartitionSize != 0.125 {
		t.Errorf("GPUPartitionSize = %v, want 0.125", result.GPUPartitionSize)
	}
	if result.LogicalGPUs != 1 {
		t.Errorf("LogicalGPUs = %d, want 1", result.LogicalGPUs)
	}
	// GPUMemoryMiB falls back to TotalGpuMemoryInMiB since the per-device
	// product (SizeInMiB * Count) is 0*2861=0 — this fallback is pre-existing
	// behavior, unchanged by this test's new assertions above.
	if result.GPUMemoryMiB != 2861 {
		t.Errorf("GPUMemoryMiB = %d, want 2861 (fallback to TotalGpuMemoryInMiB)", result.GPUMemoryMiB)
	}
}

// TestBuildResultFromEC2_NoGPU confirms non-GPU instance types leave all new
// fields at their zero value.
func TestBuildResultFromEC2_NoGPU(t *testing.T) {
	it := instanceTypeInfo("c6a.xlarge", 4, 8192, types.ArchitectureTypeX8664)

	result := buildResultFromEC2(it, "c6a.xlarge", "us-east-1")

	if result.GPUMemoryPerMiB != 0 || result.GPUPartitionSize != 0 || result.LogicalGPUs != 0 {
		t.Errorf("expected all-zero GPU fields for a non-GPU type, got GPUMemoryPerMiB=%d GPUPartitionSize=%v LogicalGPUs=%d",
			result.GPUMemoryPerMiB, result.GPUPartitionSize, result.LogicalGPUs)
	}
}

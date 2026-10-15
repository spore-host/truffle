package aws

import (
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Per-device memory as DescribeInstanceTypes reports it (us-east-1 and
// us-west-2, 2026-10-15): GpuInfo.Gpus[].MemoryInfo.SizeInMiB, or the
// Neuron device's MemoryInfo for Inferentia and Trainium.
const (
	mibT4        = 16384  // g4dn, g5g (T4g)
	mibA10G      = 22888  // g5; L4 (g6, gr6) is the same
	mibL4Half    = 11444  // g6f.4xlarge, a half L4
	mibV100      = 32768  // p3dn
	mibA100      = 40960  // p4d
	mibL40S      = 45776  // g6e
	mibH100      = 81920  // p5; A100 80 GB (p4de) is the same
	mibH200      = 144384 // p5e, p5en
	mibB200      = 183359 // p6-b200: 179 GiB (AWS sells p6-b200.48xlarge as 1440 GB for 8 GPUs)
	mibNeuronInf = 32768  // Inferentia2, Trainium
)

// gpuTypeInfo is a one-GPU instance type with gpuMiB of GPU memory.
func gpuTypeInfo(name string, gpuMiB int32) types.InstanceTypeInfo {
	it := instanceTypeInfo(name, 8, 32*1024, types.ArchitectureTypeX8664)
	it.GpuInfo = &types.GpuInfo{Gpus: []types.GpuDeviceInfo{{
		Count:      awssdk.Int32(1),
		MemoryInfo: &types.GpuDeviceMemoryInfo{SizeInMiB: awssdk.Int32(gpuMiB)},
	}}}
	return it
}

// TestNominalGPUMemoryGB checks that recorded per-GPU sizes read as the
// sizes the GPUs are sold as (#236): EC2 gives some in GiB and some as
// decimal GB converted to MiB.
func TestNominalGPUMemoryGB(t *testing.T) {
	tests := []struct {
		mib  int64
		want float64
	}{
		{mibT4, 16},
		{mibA10G, 24},
		{mibL4Half, 12},
		{mibV100, 32},
		{mibA100, 40},
		{mibL40S, 48},
		{mibH100, 80},
		{mibH200, 141},
		{mibB200, 179},
		{0, 0},
	}
	for _, tt := range tests {
		if got := NominalGPUMemoryGB(tt.mib); got < tt.want-0.1 || got > tt.want+0.1 {
			t.Errorf("NominalGPUMemoryGB(%d) = %v, want about %v", tt.mib, got, tt.want)
		}
	}
}

// TestMatchesFiltersGPUMemory checks the per-GPU memory filter against the
// recorded sizes: a 24 GB minimum keeps the A10G and L4 (22888 MiB, under
// 24 GiB) and larger GPUs, and drops the 16 GB T4 and a half L4 (#236).
func TestMatchesFiltersGPUMemory(t *testing.T) {
	trn := instanceTypeInfo("trn1.2xlarge", 8, 32*1024, types.ArchitectureTypeX8664)
	trn.NeuronInfo = &types.NeuronInfo{NeuronDevices: []types.NeuronDeviceInfo{{
		Count:      awssdk.Int32(1),
		MemoryInfo: &types.NeuronDeviceMemoryInfo{SizeInMiB: awssdk.Int32(mibNeuronInf)},
	}}}
	cpu := instanceTypeInfo("m7i.2xlarge", 8, 32*1024, types.ArchitectureTypeX8664)

	tests := []struct {
		name string
		it   types.InstanceTypeInfo
		opts FilterOptions
		want bool
	}{
		{"24gb keeps A10G", gpuTypeInfo("g5.xlarge", mibA10G), FilterOptions{MinGPUMemory: 24}, true},
		{"24gb keeps L40S", gpuTypeInfo("g6e.xlarge", mibL40S), FilterOptions{MinGPUMemory: 24}, true},
		{"24gb keeps H100", gpuTypeInfo("p5.4xlarge", mibH100), FilterOptions{MinGPUMemory: 24}, true},
		{"24gb drops T4", gpuTypeInfo("g4dn.xlarge", mibT4), FilterOptions{MinGPUMemory: 24}, false},
		{"24gb drops half L4", gpuTypeInfo("g6f.4xlarge", mibL4Half), FilterOptions{MinGPUMemory: 24}, false},
		{"24gb drops CPU-only", cpu, FilterOptions{MinGPUMemory: 24}, false},
		{"24gb keeps Trainium", trn, FilterOptions{MinGPUMemory: 24}, true},
		{"80gb keeps H100", gpuTypeInfo("p5.48xlarge", mibH100), FilterOptions{MinGPUMemory: 80}, true},
		{"80gb drops A100 40GB", gpuTypeInfo("p4d.24xlarge", mibA100), FilterOptions{MinGPUMemory: 80}, false},
		{"exact 24gb keeps A10G", gpuTypeInfo("g5.xlarge", mibA10G), FilterOptions{MinGPUMemory: 24, ExactGPUMemory: true}, true},
		{"exact 24gb drops L40S", gpuTypeInfo("g6e.xlarge", mibL40S), FilterOptions{MinGPUMemory: 24, ExactGPUMemory: true}, false},
		{"exact 80gb keeps H100", gpuTypeInfo("p5.48xlarge", mibH100), FilterOptions{MinGPUMemory: 80, ExactGPUMemory: true}, true},
		{"no GPU filter keeps CPU-only", cpu, FilterOptions{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesFilters(tt.it, tt.opts); got != tt.want {
				t.Errorf("matchesFilters = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBuildResultFromEC2_AccelMemory checks the per-device memory recorded
// on a result, for a GPU and a Neuron device.
func TestBuildResultFromEC2_AccelMemory(t *testing.T) {
	if got := buildResultFromEC2(gpuTypeInfo("g5.xlarge", mibA10G), "g5.xlarge", "us-east-1").AccelMemoryMiB; got != mibA10G {
		t.Errorf("g5 AccelMemoryMiB = %d, want %d", got, mibA10G)
	}
	inf := instanceTypeInfo("inf2.xlarge", 4, 16*1024, types.ArchitectureTypeX8664)
	inf.NeuronInfo = &types.NeuronInfo{NeuronDevices: []types.NeuronDeviceInfo{{
		Count:      awssdk.Int32(1),
		MemoryInfo: &types.NeuronDeviceMemoryInfo{SizeInMiB: awssdk.Int32(mibNeuronInf)},
	}}}
	if got := buildResultFromEC2(inf, "inf2.xlarge", "us-east-1").AccelMemoryMiB; got != mibNeuronInf {
		t.Errorf("inf2 AccelMemoryMiB = %d, want %d", got, mibNeuronInf)
	}
	cpu := instanceTypeInfo("m7i.large", 2, 8*1024, types.ArchitectureTypeX8664)
	if got := buildResultFromEC2(cpu, "m7i.large", "us-east-1").AccelMemoryMiB; got != 0 {
		t.Errorf("m7i AccelMemoryMiB = %d, want 0", got)
	}
}

// TestBuildResultFromEC2_AccelMemoryGB checks the nominal GB recorded beside
// the raw MiB (#238): the figure the filter compares, 24 for an A10G.
func TestBuildResultFromEC2_AccelMemoryGB(t *testing.T) {
	for _, tt := range []struct {
		it   string
		mib  int32
		want float64
	}{{"g5.xlarge", mibA10G, 24}, {"p5.48xlarge", mibH100, 80}} {
		r := buildResultFromEC2(gpuTypeInfo(tt.it, tt.mib), tt.it, "us-east-1")
		if r.AccelMemoryGB != tt.want || r.AccelMemoryMiB != int64(tt.mib) {
			t.Errorf("%s: AccelMemoryGB = %v, AccelMemoryMiB = %d; want %v, %d", tt.it, r.AccelMemoryGB, r.AccelMemoryMiB, tt.want, tt.mib)
		}
	}
}

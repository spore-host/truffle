package aws

import (
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/metadata"
)

// TestBuildResultFromEC2_AcceleratorDevices checks the accelerator count,
// model and maker recorded beside AccelMemoryMiB (#240), shaped as
// DescribeInstanceTypes reported them on 2026-10-15: inf2.48xlarge's 12
// Inferentia2 (NeuronInfo, no maker), dl2q.24xlarge's 8 Qualcomm AI100
// (InferenceAcceleratorInfo) and a g5 A10G (GpuInfo).
func TestBuildResultFromEC2_AcceleratorDevices(t *testing.T) {
	inf := instanceTypeInfo("inf2.48xlarge", 192, 768*1024, types.ArchitectureTypeX8664)
	inf.NeuronInfo = &types.NeuronInfo{NeuronDevices: []types.NeuronDeviceInfo{{
		Count:      awssdk.Int32(12),
		Name:       awssdk.String("Inferentia2"),
		MemoryInfo: &types.NeuronDeviceMemoryInfo{SizeInMiB: awssdk.Int32(mibNeuronInf)},
	}}}
	dl := instanceTypeInfo("dl2q.24xlarge", 96, 768*1024, types.ArchitectureTypeX8664)
	dl.InferenceAcceleratorInfo = &types.InferenceAcceleratorInfo{Accelerators: []types.InferenceDeviceInfo{{
		Count:        awssdk.Int32(8),
		Name:         awssdk.String("Qualcomm AI100"),
		Manufacturer: awssdk.String("Qualcomm"),
		MemoryInfo:   &types.InferenceDeviceMemoryInfo{SizeInMiB: awssdk.Int32(16000)},
	}}}
	g5 := gpuTypeInfo("g5.xlarge", mibA10G)
	g5.GpuInfo.Gpus[0].Name = awssdk.String("A10G")
	g5.GpuInfo.Gpus[0].Manufacturer = awssdk.String("NVIDIA")

	for _, tt := range []struct {
		it           types.InstanceTypeInfo
		count        int32
		model, maker string
		gb           float64
		gpus         int32
		cpu          bool
	}{
		{it: inf, count: 12, model: "Inferentia2", gb: 32},
		{it: dl, count: 8, model: "Qualcomm AI100", maker: "Qualcomm", gb: 16},
		{it: g5, count: 1, model: "A10G", maker: "NVIDIA", gb: 24, gpus: 1},
		{it: instanceTypeInfo("m7i.large", 2, 8*1024, types.ArchitectureTypeX8664), cpu: true},
	} {
		name := string(tt.it.InstanceType)
		r := buildResultFromEC2(tt.it, name, "us-east-1")
		if r.AcceleratorCount != tt.count || r.AcceleratorModel != tt.model || r.AcceleratorManufacturer != tt.maker {
			t.Errorf("%s: accelerator = %d %q %q, want %d %q %q", name,
				r.AcceleratorCount, r.AcceleratorModel, r.AcceleratorManufacturer, tt.count, tt.model, tt.maker)
		}
		if r.AccelMemoryGB != tt.gb {
			t.Errorf("%s: AccelMemoryGB = %v, want %v", name, r.AccelMemoryGB, tt.gb)
		}
		// GPUs stays GPU-only: spawn reads it as the GPU count.
		if r.GPUs != tt.gpus {
			t.Errorf("%s: GPUs = %d, want %d", name, r.GPUs, tt.gpus)
		}
	}
}

// TestNominalGPUMemoryGB_ThousandsOfMiB: dl2q's AI100 is reported as 16000
// MiB, a 16 GB size written as MiB; it reads as 16 GB, not 16.8 (#240).
func TestNominalGPUMemoryGB_ThousandsOfMiB(t *testing.T) {
	if got := NominalGPUMemoryGB(16000); got != 16 {
		t.Errorf("NominalGPUMemoryGB(16000) = %v, want 16", got)
	}
	if !MatchesGPUMemory(16000, 16, true) {
		t.Error("dl2q's 16000 MiB should match an exact 16 GB request")
	}
}

// TestLiveAcceleratorMemoryRecorded: metadata's per-family device memory is
// what the live catalog reports, for every family whose devices are one size
// (#240). Families it leaves out on purpose are skipped.
func TestLiveAcceleratorMemoryRecorded(t *testing.T) {
	skip := map[string]bool{"g6f": true, "gr6f": true, "trn2": true}
	sizes := make(map[string]map[int64]bool) // family -> device MiB seen
	for _, it := range liveCatalog(t) {
		mib := acceleratorMemoryPerDeviceMiB(it)
		if mib == 0 {
			continue
		}
		name := string(it.InstanceType)
		family := name[:strings.IndexByte(name, '.')]
		if sizes[family] == nil {
			sizes[family] = make(map[int64]bool)
		}
		sizes[family][mib] = true
	}
	for family, seen := range sizes {
		if skip[family] {
			continue
		}
		got, ok := metadata.AcceleratorMemoryMiB(family)
		if len(seen) != 1 {
			if ok {
				t.Errorf("%s: device memory varies by size (%v) but %d MiB is recorded", family, seen, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: live device memory %v, none recorded", family, seen)
		} else if !seen[got] {
			t.Errorf("%s: live device memory %v, recorded %d MiB", family, seen, got)
		}
	}
}

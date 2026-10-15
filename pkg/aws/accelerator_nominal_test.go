package aws

import (
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Device records as DescribeInstanceTypes returned them in us-east-1,
// us-east-2 and us-west-2 on 2026-10-15 (#242).
const (
	mibB300      = 275040 // p6-b300: 268.59 GiB per GPU
	mibTrainium2 = 524288 // trn2: 512 GiB per Neuron device, as EC2 reports it
	mibInf1      = 8192   // inf1: Inferentia
)

// neuronTypeInfo is an instance type with count Neuron devices of model,
// each reported with mib MiB and cores NeuronCores.
func neuronTypeInfo(name, model string, count, cores, mib int32) types.InstanceTypeInfo {
	it := instanceTypeInfo(name, 8, 32*1024, types.ArchitectureTypeX8664)
	it.NeuronInfo = &types.NeuronInfo{NeuronDevices: []types.NeuronDeviceInfo{{
		Count:      awssdk.Int32(count),
		Name:       awssdk.String(model),
		CoreInfo:   &types.NeuronDeviceCoreInfo{Count: awssdk.Int32(cores)},
		MemoryInfo: &types.NeuronDeviceMemoryInfo{SizeInMiB: awssdk.Int32(mib)},
	}}}
	return it
}

// TestNominalGPUMemoryGB_AWSDocumented checks the B200 and B300 read as AWS
// documents them: p6-b200.48xlarge has 1,432 GB for 8 GPUs (179 each) and
// p6-b300.48xlarge 2,144 GB for 8 (268 each), not EC2's 179.06 and 268.59
// GiB (#242). The other sizes are unchanged.
func TestNominalGPUMemoryGB_AWSDocumented(t *testing.T) {
	for _, tt := range []struct {
		name string
		mib  int64
		want float64
	}{
		{"B200", mibB200, 1432.0 / 8},
		{"B300", mibB300, 2144.0 / 8},
		{"A10G", mibA10G, 24},
		{"L4", 22888, 24},
		{"H100", mibH100, 80},
		{"AI100 (dl2q)", 16000, 16},
		{"T4", mibT4, 16},
		{"Inferentia", mibInf1, 8},
		{"Inferentia2 / Trainium", mibNeuronInf, 32},
	} {
		if got := NominalGPUMemoryGB(tt.mib); got != tt.want {
			t.Errorf("%s: NominalGPUMemoryGB(%d) = %v, want %v", tt.name, tt.mib, got, tt.want)
		}
	}
}

// TestAcceleratorMemory_NeuronPerChip checks a Neuron device reads as one
// chip at its documented memory (#242): Trainium2 96 GiB per chip (EC2
// reports 512 GiB per device), and Inferentia 8, Inferentia2 and Trainium 32
// GiB, as EC2 already reports them.
func TestAcceleratorMemory_NeuronPerChip(t *testing.T) {
	for _, tt := range []struct {
		it        types.InstanceTypeInfo
		wantMiB   int64
		wantGB    float64
		wantCount int32
	}{
		{neuronTypeInfo("trn2.48xlarge", "Trainium2", 16, 8, mibTrainium2), 96 * 1024, 96, 16},
		{neuronTypeInfo("trn1.32xlarge", "Trainium", 16, 2, mibNeuronInf), mibNeuronInf, 32, 16},
		{neuronTypeInfo("inf2.48xlarge", "Inferentia2", 12, 2, mibNeuronInf), mibNeuronInf, 32, 12},
		{neuronTypeInfo("inf1.24xlarge", "Inferentia", 16, 4, mibInf1), mibInf1, 8, 16},
	} {
		name := string(tt.it.InstanceType)
		r := buildResultFromEC2(tt.it, name, "us-east-1")
		if r.AccelMemoryMiB != tt.wantMiB || r.AccelMemoryGB != tt.wantGB || r.AcceleratorCount != tt.wantCount {
			t.Errorf("%s: AccelMemoryMiB = %d, AccelMemoryGB = %v, AcceleratorCount = %d; want %d, %v, %d",
				name, r.AccelMemoryMiB, r.AccelMemoryGB, r.AcceleratorCount, tt.wantMiB, tt.wantGB, tt.wantCount)
		}
	}
}

// TestMatchesFilters_AcceleratorNominal checks the per-GPU memory filter
// compares the same documented figures (#242): a Trainium2 chip is 96 GB,
// so "trainium 256gb" no longer matches trn2, and an exact 268 GB request
// finds the B300.
func TestMatchesFilters_AcceleratorNominal(t *testing.T) {
	trn2 := neuronTypeInfo("trn2.48xlarge", "Trainium2", 16, 8, mibTrainium2)
	for _, tt := range []struct {
		name string
		it   types.InstanceTypeInfo
		opts FilterOptions
		want bool
	}{
		{"96gb keeps trn2", trn2, FilterOptions{MinGPUMemory: 96}, true},
		{"exact 96gb keeps trn2", trn2, FilterOptions{MinGPUMemory: 96, ExactGPUMemory: true}, true},
		{"256gb drops trn2", trn2, FilterOptions{MinGPUMemory: 256}, false},
		{"179gb keeps B200", gpuTypeInfo("p6-b200.48xlarge", mibB200), FilterOptions{MinGPUMemory: 179}, true},
		{"exact 179gb keeps B200", gpuTypeInfo("p6-b200.48xlarge", mibB200), FilterOptions{MinGPUMemory: 179, ExactGPUMemory: true}, true},
		{"exact 268gb keeps B300", gpuTypeInfo("p6-b300.48xlarge", mibB300), FilterOptions{MinGPUMemory: 268, ExactGPUMemory: true}, true},
		{"269gb drops B300", gpuTypeInfo("p6-b300.48xlarge", mibB300), FilterOptions{MinGPUMemory: 269}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesFilters(tt.it, tt.opts); got != tt.want {
				t.Errorf("matchesFilters = %v, want %v", got, tt.want)
			}
		})
	}
}

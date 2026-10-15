package aws

import (
	"math"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// acceleratorMemoryPerDeviceMiB returns the memory of one GPU or accelerator
// device of an instance type, in MiB, as DescribeInstanceTypes reports it:
// GpuInfo.Gpus[].MemoryInfo for GPUs (one partition's memory for a
// fractional GPU such as g6f), else NeuronInfo.NeuronDevices[].MemoryInfo
// for Inferentia and Trainium, else InferenceAcceleratorInfo for other
// inference accelerators (dl2q). It is 0 when the type has none of these
// (#236).
func acceleratorMemoryPerDeviceMiB(it types.InstanceTypeInfo) int64 {
	var best int64
	consider := func(p *int32) {
		if v := int64(valueOrZero(p)); v > best {
			best = v
		}
	}
	if it.GpuInfo != nil {
		for _, g := range it.GpuInfo.Gpus {
			if g.MemoryInfo != nil {
				consider(g.MemoryInfo.SizeInMiB)
			}
		}
	}
	if best == 0 && it.NeuronInfo != nil {
		for _, d := range it.NeuronInfo.NeuronDevices {
			if d.MemoryInfo != nil {
				consider(d.MemoryInfo.SizeInMiB)
			}
		}
	}
	if best == 0 && it.InferenceAcceleratorInfo != nil {
		for _, a := range it.InferenceAcceleratorInfo.Accelerators {
			if a.MemoryInfo != nil {
				consider(a.MemoryInfo.SizeInMiB)
			}
		}
	}
	return best
}

// NominalGPUMemoryGB converts a device's reported memory (MiB) to the size it
// is sold as, in GB (#236). EC2 reports some GPUs in binary units (H100
// 81920 MiB = 80 GiB, T4 16384 MiB = 16 GiB) and others as a decimal size
// converted to MiB (A10G and L4 22888 MiB = 24 GB, L40S 45776 MiB = 48 GB),
// so the GiB figure is used when it is a whole number (within 0.5%) and the
// decimal-GB figure, to 0.1 GB, otherwise. A "24gb" GPU query then matches
// the A10G and L4, which a plain GiB comparison (22.35 GiB) would miss.
func NominalGPUMemoryGB(mib int64) float64 {
	if mib <= 0 {
		return 0
	}
	gib := float64(mib) / 1024
	if r := math.Round(gib); r > 0 && math.Abs(gib-r)/r <= 0.005 {
		return gib
	}
	// To 0.1 GB, so a 24 GB GPU reads as exactly 24 (22888 MiB is 23.9998 GB).
	return math.Round(float64(mib)*(1<<20)/1e8) / 10
}

// gpuMemoryTolerance absorbs rounding in the nominal size: 0.05 GB is well
// below the gap between any two GPU memory sizes.
const gpuMemoryTolerance = 0.05

// MatchesGPUMemory reports whether a device of mib MiB satisfies a per-GPU
// memory request of wantGB: at least wantGB nominal GB, or about exactly
// wantGB (within half a GB) when exact is set. It mirrors the host-memory
// filter's minimum / --exact semantics.
func MatchesGPUMemory(mib int64, wantGB float64, exact bool) bool {
	got := NominalGPUMemoryGB(mib)
	if got == 0 {
		return false
	}
	if exact {
		return math.Abs(got-wantGB) < 0.5
	}
	return got >= wantGB-gpuMemoryTolerance
}

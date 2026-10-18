package aws

import (
	"math"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// acceleratorMemoryPerDeviceMiB returns the memory of one GPU or accelerator
// device of an instance type, in MiB, as DescribeInstanceTypes reports it:
// GpuInfo.Gpus[].MemoryInfo for GPUs (one partition's memory for a
// fractional GPU such as g6f), else NeuronInfo.NeuronDevices[].MemoryInfo
// for Inferentia and Trainium, else InferenceAcceleratorInfo for other
// inference accelerators (dl2q). It is 0 when the type has none of these
// (#236). A Neuron device whose EC2 record is wrong reads as its documented
// per-chip size instead (see neuronChipMemoryMiB; #242).
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
			if documented, ok := neuronChipMemoryMiB[valueOrZero(d.Name)]; ok {
				if documented > best {
					best = documented
				}
			} else if d.MemoryInfo != nil {
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

// neuronChipMemoryMiB is the documented memory of one chip of a Neuron
// device model whose DescribeInstanceTypes record is wrong, keyed by the
// NeuronInfo device name (#242). A NeuronInfo device is one chip: its
// CoreInfo count is the chip's NeuronCores (2 for Inferentia2 and Trainium,
// 4 for Inferentia, 8 for Trainium2), and its memory is the chip's for
// inf1 (8 GiB), inf2 and trn1 (32 GiB), as the Neuron architecture docs
// give them. For Trainium2, EC2 reports 524288 MiB (512 GiB) per device and
// 8 TiB for trn2.48xlarge's 16, but a Trainium2 chip has 96 GiB of HBM
// (https://awsdocs-neuron.readthedocs-hosted.com/en/latest/about-neuron/arch/neuron-hardware/trainium2.html)
// and trn2.48xlarge 1.5 TB across its 16 chips
// (https://aws.amazon.com/ec2/instance-types/trn2/).
var neuronChipMemoryMiB = map[string]int64{
	"Trainium2": 96 * 1024,
}

// nominalGBOverride is the size a GPU model is sold as on EC2 where the
// generic reading of its reported MiB below lands off it, keyed by the exact
// per-GPU MiB that model reports (so only that model's record is affected;
// the 185 GiB B200 of p6e-gb200 is not). The figures are AWS's documented
// per-instance GPU memory divided by the GPU count (#242): p6-b200.48xlarge
// 1,432 GB for 8 B200s and p6-b300.48xlarge 2,144 GB for 8 B300s
// (https://aws.amazon.com/ec2/instance-types/p6/), which the EC2
// instance-type specifications list as 8 x 179 GiB and 8 x 268 GiB
// (https://docs.aws.amazon.com/ec2/latest/instancetypes/ac.html). They are
// binary sizes, a whole number of GiB of the reported MiB, so they are
// labelled GiB (#244).
var nominalGBOverride = map[int64]float64{
	183359: 179, // NVIDIA B200, p6-b200 (EC2 reads 179.06 GiB)
	275040: 268, // NVIDIA B300, p6-b300 (EC2 reads 268.59 GiB)
}

// Units of a nominal GPU memory size (#244).
const (
	UnitGiB = "GiB" // binary: 2^30 bytes
	UnitGB  = "GB"  // decimal: 10^9 bytes
)

// NominalGPUMemoryGB converts a device's reported memory (MiB) to the size it
// is sold as (#236). Despite the name, the figure is not always decimal GB:
// NominalGPUMemoryUnit says which unit it is in (#244). EC2 reports some GPUs
// in binary units (H100 81920 MiB = 80 GiB, T4 16384 MiB = 16 GiB) and others
// as a decimal size converted to MiB (A10G and L4 22888 MiB = 24 GB, L40S
// 45776 MiB = 48 GB), so the GiB figure is used when it is a whole number
// (within 0.5%) and the decimal-GB figure, to 0.1 GB, otherwise. A "24gb"
// GPU query then matches the A10G and L4, which a plain GiB comparison
// (22.35 GiB) would miss. A size that is a whole number of thousands of MiB
// and not of GiB is a GB figure recorded as MiB, as for dl2q's 16 GB
// Qualcomm AI100 (16000 MiB), and reads as that many GB (#240). A model in
// nominalGBOverride (B200, B300) reads as its AWS-documented size, which
// takes precedence (#242).
func NominalGPUMemoryGB(mib int64) float64 {
	size, _ := nominalGPUMemory(mib)
	return size
}

// NominalGPUMemoryUnit returns the unit NominalGPUMemoryGB(mib) is in:
// UnitGiB when the device is sized in binary units (H100 80 GiB, B200 179
// GiB), UnitGB when in decimal ones (A10G 24 GB, dl2q's AI100 16 GB), and
// "" when mib is not positive (#244).
func NominalGPUMemoryUnit(mib int64) string {
	_, unit := nominalGPUMemory(mib)
	return unit
}

// FormatGPUMemory renders a device's nominal memory with its unit, to at
// most one decimal place: "80 GiB", "24 GB", "179 GiB"; "" when mib is not
// positive (#244).
func FormatGPUMemory(mib int64) string {
	size, unit := nominalGPUMemory(mib)
	if unit == "" {
		return ""
	}
	return FormatSize(size) + " " + unit
}

// FormatSize renders a memory size to at most one decimal place: "24", "80",
// "2.8", "179.1"; never "24.0".
func FormatSize(size float64) string {
	return strconv.FormatFloat(math.Round(size*10)/10, 'f', -1, 64)
}

// nominalGPUMemory is NominalGPUMemoryGB and NominalGPUMemoryUnit.
func nominalGPUMemory(mib int64) (float64, string) {
	if mib <= 0 {
		return 0, ""
	}
	if gib, ok := nominalGBOverride[mib]; ok {
		return gib, UnitGiB
	}
	gib := float64(mib) / 1024
	if r := math.Round(gib); r > 0 && math.Abs(gib-r)/r <= 0.005 {
		return gib, UnitGiB
	}
	if mib%1000 == 0 {
		return float64(mib / 1000), UnitGB
	}
	// To 0.1 GB, so a 24 GB GPU reads as exactly 24 (22888 MiB is 23.9998 GB).
	return math.Round(float64(mib)*(1<<20)/1e8) / 10, UnitGB
}

// gpuMemoryTolerance absorbs rounding in the nominal size: 0.05 GB is well
// below the gap between any two GPU memory sizes.
const gpuMemoryTolerance = 0.05

// MatchesGPUMemory reports whether a device of mib MiB satisfies a per-GPU
// memory request of wantGB: at least wantGB of the nominal size, the number
// NominalGPUMemoryGB gives in the unit the device is sold in (GiB for an
// H100's 80, GB for an A10G's 24; #244), or about exactly
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

// acceleratorDevices returns the number, model and maker of an instance
// type's GPUs or accelerator devices, from the same record
// acceleratorMemoryPerDeviceMiB reads: GpuInfo for GPUs, else NeuronInfo for
// Inferentia and Trainium (which reports no manufacturer), else
// InferenceAcceleratorInfo (dl2q). A fractional GPU (g6f) has count 0, as
// GpuInfo reports it. The model is the last device entry's name, as for
// InstanceTypeResult.GPUModel (#240).
func acceleratorDevices(it types.InstanceTypeInfo) (count int32, model, manufacturer string) {
	if it.GpuInfo != nil && len(it.GpuInfo.Gpus) > 0 {
		for _, g := range it.GpuInfo.Gpus {
			count += valueOrZero(g.Count)
			model = valueOrZero(g.Name)
			manufacturer = valueOrZero(g.Manufacturer)
		}
		return count, model, manufacturer
	}
	if it.NeuronInfo != nil && len(it.NeuronInfo.NeuronDevices) > 0 {
		for _, d := range it.NeuronInfo.NeuronDevices {
			count += valueOrZero(d.Count)
			model = valueOrZero(d.Name)
		}
		return count, model, ""
	}
	if it.InferenceAcceleratorInfo != nil && len(it.InferenceAcceleratorInfo.Accelerators) > 0 {
		for _, a := range it.InferenceAcceleratorInfo.Accelerators {
			count += valueOrZero(a.Count)
			model = valueOrZero(a.Name)
			manufacturer = valueOrZero(a.Manufacturer)
		}
		return count, model, manufacturer
	}
	return 0, "", ""
}

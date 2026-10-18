package find

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/metadata"
)

// FindResult extends [aws.InstanceTypeResult] with match explanations produced
// by [ExplainMatch], useful for displaying to end users why a result was returned.
type FindResult struct {
	aws.InstanceTypeResult          // Embedded full result from the AWS query
	MatchReasons           []string // Human-readable explanations of why this instance matched the query
	MatchScore             int      // Relevance score for sorting; higher means more specific match
}

// ExplainMatch generates human-readable reasons why an instance matched the query
func ExplainMatch(result aws.InstanceTypeResult, query *ParsedQuery) []string {
	var reasons []string

	family := extractFamily(result.InstanceType)

	// Check processor match
	for _, proc := range query.Processors {
		if info, ok := metadata.ProcessorDatabase[proc]; ok {
			for _, f := range info.Families {
				if f == family {
					reasons = append(reasons,
						fmt.Sprintf("Processor: %s (%s %s)",
							info.CodeName, info.Vendor, info.Generation))
					goto processorDone
				}
			}
		}
	}
processorDone:

	// Check vendor match
	for _, vendor := range query.Vendors {
		if metadata.VendorOfFamily(family) == vendor {
			reasons = append(reasons, fmt.Sprintf("Vendor: %s", vendor))
			break
		}
	}

	// Check GPU match
	for _, gpu := range query.GPUs {
		if info, ok := metadata.GPUDatabase[gpu]; ok {
			// Check exact instance type match
			for _, inst := range info.InstanceTypes {
				if inst == result.InstanceType {
					reasons = append(reasons,
						fmt.Sprintf("GPU: %s (%s, %s)",
							info.Name, gpuMemoryLabel(info, family), info.UseCase))
					goto gpuDone
				}
			}
			// Check family match
			for _, f := range info.Families {
				if f == family {
					reasons = append(reasons,
						fmt.Sprintf("GPU family: %s (%s)",
							info.Name, info.UseCase))
					goto gpuDone
				}
			}
		}
	}
gpuDone:

	// Check accelerator-word match ("gpu", "training", ...), #233
	if len(query.Accelerators) > 0 {
		for _, f := range query.acceleratorFamilies() {
			if f == family {
				reasons = append(reasons, fmt.Sprintf("Accelerator: %s", strings.Join(query.Accelerators, ", ")))
				break
			}
		}
	}

	// Check instruction-set match
	for _, is := range query.InstructionSets {
		for _, f := range metadata.GetFamiliesByInstructionSet(is) {
			if f == family {
				if info, ok := metadata.InstructionSetDatabase[is]; ok {
					reasons = append(reasons, fmt.Sprintf("Instruction set: %s", info.Name))
				}
				goto instructionSetDone
			}
		}
	}
instructionSetDone:

	// Check size match
	if dot := strings.LastIndexByte(result.InstanceType, '.'); dot >= 0 {
		typeSize := result.InstanceType[dot+1:]
		for _, size := range query.Sizes {
			if metadata.InSizeCategory(typeSize, size) {
				reasons = append(reasons, fmt.Sprintf("Size: %s", size))
				break
			}
		}
	}

	// Check vCPU match
	if query.MinVCPU > 0 && result.VCPUs >= int32(query.MinVCPU) {
		reasons = append(reasons,
			fmt.Sprintf("vCPUs: %d >= %d", result.VCPUs, query.MinVCPU))
	}

	// Check memory match
	if query.MinMemory > 0 {
		memGiB := float64(result.MemoryMiB) / 1024.0
		if memGiB >= query.MinMemory {
			reasons = append(reasons,
				fmt.Sprintf("Memory: %.0f GiB >= %.0f GiB", memGiB, query.MinMemory))
		}
	}

	// Check per-GPU memory match (#236)
	if query.MinGPUMemory > 0 && aws.MatchesGPUMemory(result.AccelMemoryMiB, query.MinGPUMemory, query.ExactMatch) {
		// The device's size carries its unit (GiB or GB, as it is sold);
		// the request is compared with that number (#244).
		reasons = append(reasons, fmt.Sprintf("GPU memory: %s >= %s",
			aws.FormatGPUMemory(result.AccelMemoryMiB),
			strconv.FormatFloat(query.MinGPUMemory, 'f', -1, 64)))
	}

	// Check architecture match
	if query.Architecture != "" && strings.EqualFold(result.Architecture, query.Architecture) {
		reasons = append(reasons, fmt.Sprintf("Architecture: %s", result.Architecture))
	}

	// Check EFA match
	if query.RequireEFA && metadata.IsEFASupported(family) {
		reasons = append(reasons, "Network: EFA supported")
	}

	// Check MIG match
	if query.RequireMIG && metadata.IsMIGSupported(family) {
		reasons = append(reasons, "GPU: MIG-capable")
	}

	// Check network speed match
	if query.MinNetworkGbps > 0 {
		// Check if family supports the required bandwidth
		for speed, capability := range metadata.NetworkBandwidthTiers {
			if capability.MaxBandwidthGbps >= query.MinNetworkGbps {
				for _, f := range capability.Families {
					if f == family {
						reasons = append(reasons,
							fmt.Sprintf("Network: %d+ Gbps (supports %s)", query.MinNetworkGbps, speed))
						goto networkDone
					}
				}
			}
		}
	}
networkDone:

	return reasons
}

// gpuMemoryLabel is the per-device memory of GPU model info in family as
// sold, with its unit: the family's recorded figure
// (metadata.AcceleratorMemoryMiB), so a p3dn V100 reads 32 GiB and a p4de
// A100 80 GiB, an A10G 24 GB (#240, #244), else the model's MemoryGB in the
// unit the model's recorded families are sold in (a p3 V100 16 GiB, a g6f
// L4 24 GB), or GB if none is recorded.
func gpuMemoryLabel(info metadata.GPUInfo, family string) string {
	if mib, ok := metadata.AcceleratorMemoryMiB(family); ok {
		return aws.FormatGPUMemory(mib)
	}
	unit := aws.UnitGB
	for _, f := range info.Families {
		if mib, ok := metadata.AcceleratorMemoryMiB(f); ok {
			unit = aws.NominalGPUMemoryUnit(mib)
			break
		}
	}
	return strconv.Itoa(info.MemoryGB) + " " + unit
}

// extractFamily extracts the instance family from an instance type
// e.g., "m6i.2xlarge" -> "m6i"
func extractFamily(instanceType string) string {
	parts := strings.Split(instanceType, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

package metadata

// InstructionSetInfo describes a CPU instruction-set extension and which
// instance families provide it. AWS's DescribeInstanceTypes API does not
// expose instruction-set data at all (the same limitation ProcessorDatabase
// and GPUDatabase already work around) — this is a hand-maintained, derived
// view of ProcessorDatabase's own family lists, cross-referenced against each
// processor generation's publicly documented instruction-set support.
type InstructionSetInfo struct {
	Name         string   // "AVX2", "AVX-512", "SVE", "SVE2"
	Architecture string   // "x86_64" or "arm64" — instruction sets are architecture-specific
	Families     []string // Instance families that provide this instruction set
}

// instructionSetProcessors maps each instruction set to the ProcessorDatabase
// generation keys that provide it. Deriving family lists from ProcessorDatabase
// at init time — rather than duplicating a second hardcoded family list here —
// means a correction to ProcessorDatabase (e.g. a missing AZ/storage variant
// suffix) automatically propagates to instruction-set search without a second
// place to remember to update.
//
// Sources (checked 2026-08): AWS Graviton Getting Started guide's processor
// feature-comparison table (github.com/aws/aws-graviton-getting-started) for
// SVE/SVE2 — SVE arrives with Graviton3(E), SVE2 with Graviton4, and Graviton5
// carries SVE2 forward (its own row adds unrelated features, not a new SVE
// revision). Wikipedia's AVX-512 CPU-support table for Intel Xeon Scalable and
// AMD EPYC: AVX-512 arrives with Ice Lake on Intel and Genoa/Zen4 on AMD;
// Skylake/Cascade Lake have only a baseline AVX-512 predating the VNNI/GFNI/
// VAES extensions most workloads care about, so they're excluded here to keep
// the term meaningful. AVX2 arrives with Haswell on Intel; every AMD EPYC
// generation AWS offers (Rome/Zen2 onward) already has it.
var instructionSetProcessors = map[string][]string{
	"avx2":    {"haswell", "broadwell", "cascade lake", "skylake", "ice lake", "sapphire rapids", "emerald rapids", "granite rapids", "rome", "milan", "zen 3", "genoa", "bergamo", "zen 4", "turin", "zen 5"},
	"avx-512": {"ice lake", "sapphire rapids", "emerald rapids", "granite rapids", "genoa", "bergamo", "zen 4", "turin", "zen 5"},
	"sve":     {"graviton3", "graviton3e"},
	"sve2":    {"graviton4", "graviton5"},
}

// InstructionSetDatabase maps instruction-set names to their information,
// built from instructionSetProcessors + ProcessorDatabase at package init.
var InstructionSetDatabase = buildInstructionSetDatabase()

func buildInstructionSetDatabase() map[string]InstructionSetInfo {
	names := map[string]string{"avx2": "AVX2", "avx-512": "AVX-512", "sve": "SVE", "sve2": "SVE2"}
	archs := map[string]string{"avx2": "x86_64", "avx-512": "x86_64", "sve": "arm64", "sve2": "arm64"}

	db := make(map[string]InstructionSetInfo, len(instructionSetProcessors))
	for key, procKeys := range instructionSetProcessors {
		familySet := make(map[string]bool)
		for _, procKey := range procKeys {
			if info, ok := ProcessorDatabase[procKey]; ok {
				for _, f := range info.Families {
					familySet[f] = true
				}
			}
		}
		families := make([]string, 0, len(familySet))
		for f := range familySet {
			families = append(families, f)
		}
		db[key] = InstructionSetInfo{Name: names[key], Architecture: archs[key], Families: families}
	}
	return db
}

// InstructionSetAliases maps common spellings to canonical InstructionSetDatabase keys.
var InstructionSetAliases = map[string]string{
	"avx512":   "avx-512",
	"avx-512f": "avx-512",
	"avx 512":  "avx-512", // two-word spelling; requires the multi-word phrase matcher
}

// GetFamiliesByInstructionSet returns the instance families that provide the
// named instruction set. name must already be the canonical key (see
// InstructionSetAliases for resolving user-facing spellings).
func GetFamiliesByInstructionSet(name string) []string {
	if info, ok := InstructionSetDatabase[name]; ok {
		return info.Families
	}
	return nil
}

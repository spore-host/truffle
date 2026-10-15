// Package find implements the natural language EC2 instance search pipeline
// used by the truffle tool. It covers three stages:
//
//  1. Parsing: [ParseQuery] converts a free-text query ("nvidia h100 8gpu",
//     "amd epyc genoa 64gb memory") into a structured [ParsedQuery].
//
//  2. Criteria building: [ParsedQuery.BuildCriteria] translates the parsed
//     query into a [SearchCriteria] containing a compiled regexp and
//     [FilterOptions] ready to pass to truffle/pkg/aws.SearchInstanceTypes.
//
//  3. Result enrichment: [ExplainMatch] annotates each result with
//     human-readable match reasons (e.g., "GPU: A100 (80 GiB, training)").
//
// Typical usage:
//
//	pq, err := find.ParseQuery("nvidia h100 8gpu")
//	criteria, err := pq.BuildCriteria(true) // true = include AZs (search-time lookup)
//	results, err := client.SearchInstanceTypes(ctx, regions, criteria.InstanceTypePattern, criteria.FilterOptions)
//	for _, r := range results {
//	    reasons := find.ExplainMatch(r, pq)
//	}
package find

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spore-host/libs/catalog"
	"github.com/spore-host/truffle/pkg/metadata"
)

// TokenType represents the type of a parsed token
type TokenType int

const (
	TokenUnknown TokenType = iota
	TokenVendor
	TokenProcessor
	TokenGPU
	TokenSize
	TokenVCPU
	TokenMemory
	TokenGPUCount
	TokenArchitecture
	TokenNetworkSpeed
	TokenEFA
	TokenNestedVirt     // Nested-virtualization support (e.g. "nested-virt")
	TokenPhysicalCores  // Physical core count (e.g. "8 physical cores")
	TokenApp            // Application name from pkg/catalog (e.g. "paraview", "igv")
	TokenQualitative    // Qualitative/subjective keyword (e.g. "cheap", "fastest")
	TokenInstructionSet // CPU instruction-set extension (e.g. "avx2", "avx-512", "sve", "sve2")
	TokenMIG            // NVIDIA Multi-Instance GPU capability (e.g. "mig")
	TokenAnd            // Explicit "and" keyword — combine dimensions with intersection (default)
	TokenOr             // Explicit "or" keyword — combine dimensions with union
	TokenFamily         // Instance family name (e.g. "g5", "m7i-flex"), #229
	TokenInstanceSize   // Instance size name that is not a size word (e.g. "xlarge", "2xlarge", "metal-24xl"), #229
	TokenAccelerator    // Any GPU or accelerator ("gpu", "accelerators"), or one for a use case ("training", "inference"), #233
	TokenHostMemory     // A memory size marked as host memory ("64gb ram", "64gb memory"), #236
	TokenGPUMemory      // A memory size marked as per-GPU memory ("24gb vram", "24gb gpu memory"), #236
)

// Token represents a single classified word from a natural language query.
type Token struct {
	Type  TokenType // Semantic classification of this token
	Value string    // Normalized canonical value, e.g. "nvidia", "128gb"
	Raw   string    // Original input text before normalization
}

// ParsedQuery is the structured output of [ParseQuery]. It holds all constraints
// extracted from the user's free-text input and is consumed by [ParsedQuery.BuildCriteria].
type ParsedQuery struct {
	Vendors         []string          // Hardware vendor filters, e.g. ["amd"], ["nvidia"]
	Processors      []string          // Processor code names, e.g. ["genoa", "sapphire rapids"]
	GPUs            []string          // GPU model names, e.g. ["h100", "a100"]
	Accelerators    []string          // Accelerator words: "any" (gpu, accelerator), "training" or "inference" (#233)
	InstructionSets []string          // CPU instruction-set extensions, e.g. ["avx-512", "sve2"]
	Sizes           []string          // Size-category filters, e.g. ["large", "huge"]
	Families        []string          // Instance families named directly, e.g. ["g5"] (#229)
	InstanceSizes   []string          // Exact instance sizes named directly, e.g. ["xlarge", "2xlarge"] (#229)
	MinVCPU         int               // Minimum vCPU count; 0 means unconstrained
	MinPhysCores    int               // Minimum physical core count; 0 means unconstrained
	MinMemory       float64           // Minimum memory in GiB; 0 means unconstrained
	MinGPUMemory    float64           // Minimum memory of each GPU or accelerator, in GB as the GPU is sold; 0 means unconstrained (#236)
	GPUMemoryRead   string            // The bare size ("24gb") a GPU query read as per-GPU memory rather than host memory; empty if none (#236)
	GPUCount        int               // Minimum number of GPUs; 0 means unconstrained
	Architecture    string            // "x86_64" or "arm64"; empty means both
	MinNetworkGbps  int               // Minimum network bandwidth in Gbps; 0 means unconstrained
	RequireEFA      bool              // If true, only match instance families with EFA support
	RequireMIG      bool              // If true, only match GPU families supporting NVIDIA Multi-Instance GPU (#143)
	RequireNestedV  bool              // If true, only match instance types supporting nested virtualization
	ExactMatch      bool              // If true, match exact vCPU and memory values instead of minimum
	RawTokens       []Token           // Parsed tokens in input order, useful for diagnostics
	Apps            []string          // Application names from catalog (e.g. ["paraview"]); resolved to hardware in BuildCriteria
	Operator        DimensionOperator // How cross-dimension constraints combine (#144); AND is the default (zero value)
}

// DimensionOperator controls how the distinct query dimensions (vendor,
// processor, GPU, instruction set, EFA, MIG, network speed) combine in
// [ParsedQuery.ResolveInstanceFamilies]. Within a single dimension, values
// always stay OR'd (e.g. "a100 h100" always means A100-or-H100) — this
// operator only governs combination *across* dimensions (e.g. whether
// "h100 efa" means H100-and-EFA or H100-or-EFA).
type DimensionOperator int

const (
	OperatorAnd DimensionOperator = iota // default: intersect across dimensions
	OperatorOr                           // explicit "or": union across dimensions
)

var (
	numberRegex       = regexp.MustCompile(`^\d+$`)
	memoryRegex       = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(gb|gib|g)$`)
	networkSpeedRegex = regexp.MustCompile(`^(\d+)\s*(gbps|g)$`)
)

// maxPhraseWords is the longest multi-word key across the processor/GPU catalogs
// and GPU aliases, computed from the data rather than hardcoded so the phrase
// matcher stays correct as the catalog grows. Without this, a canonical key like
// "rtx pro server 6000" (4 words) was unreachable by a 2/3-word-only lookahead
// and only matched via a single-token alias (#90).
var maxPhraseWords = computeMaxPhraseWords()

func computeMaxPhraseWords() int {
	max := 1
	consider := func(key string) {
		if n := len(strings.Fields(key)); n > max {
			max = n
		}
	}
	for k := range metadata.ProcessorDatabase {
		consider(k)
	}
	for k := range metadata.GPUDatabase {
		consider(k)
	}
	for k := range metadata.GPUAliases {
		consider(k)
	}
	for k := range metadata.InstructionSetDatabase {
		consider(k)
	}
	for k := range metadata.InstructionSetAliases {
		consider(k)
	}
	return max
}

// matchPhrase tries the longest multi-word phrase starting at words[i], from
// maxPhraseWords down to 2 words, against the processor DB, GPU DB, and GPU
// aliases (in that precedence). On a hit it returns the token and how many words
// it consumed. Checking aliases here — not just the single-word path — lets
// multi-word marketing spellings (e.g. "rtx pro 6000") resolve exactly.
func matchPhrase(words []string, i int) (Token, int, bool) {
	remaining := len(words) - i
	upper := maxPhraseWords
	if upper > remaining {
		upper = remaining
	}
	for n := upper; n >= 2; n-- {
		phrase := strings.Join(words[i:i+n], " ")
		if _, ok := metadata.ProcessorDatabase[phrase]; ok {
			return Token{Type: TokenProcessor, Value: phrase, Raw: phrase}, n, true
		}
		if _, ok := metadata.GPUDatabase[phrase]; ok {
			return Token{Type: TokenGPU, Value: phrase, Raw: phrase}, n, true
		}
		if alias, ok := metadata.GPUAliases[phrase]; ok {
			return Token{Type: TokenGPU, Value: alias, Raw: phrase}, n, true
		}
		if _, ok := metadata.InstructionSetDatabase[phrase]; ok {
			return Token{Type: TokenInstructionSet, Value: phrase, Raw: phrase}, n, true
		}
		if alias, ok := metadata.InstructionSetAliases[phrase]; ok {
			return Token{Type: TokenInstructionSet, Value: alias, Raw: phrase}, n, true
		}
	}
	return Token{}, 0, false
}

// ParseQuery parses a natural language query into structured search criteria
func ParseQuery(query string) (*ParsedQuery, error) {
	// Normalize: lowercase, trim
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}

	// Tokenize: split on whitespace
	words := strings.Fields(query)

	// Classify tokens
	tokens := classifyTokens(words)

	// Build ParsedQuery
	pq := &ParsedQuery{RawTokens: tokens}
	var bareMemory float64
	var bareMemoryRaw string
	for _, token := range tokens {
		switch token.Type {
		case TokenVendor:
			pq.Vendors = append(pq.Vendors, token.Value)
		case TokenProcessor:
			pq.Processors = append(pq.Processors, token.Value)
		case TokenGPU:
			pq.GPUs = append(pq.GPUs, token.Value)
		case TokenAccelerator:
			pq.Accelerators = append(pq.Accelerators, token.Value)
		case TokenInstructionSet:
			pq.InstructionSets = append(pq.InstructionSets, token.Value)
		case TokenSize:
			pq.Sizes = append(pq.Sizes, token.Value)
		case TokenFamily:
			pq.Families = append(pq.Families, token.Value)
		case TokenInstanceSize:
			pq.InstanceSizes = append(pq.InstanceSizes, token.Value)
		case TokenVCPU:
			if v, err := strconv.Atoi(token.Value); err == nil {
				pq.MinVCPU = v
			}
		case TokenPhysicalCores:
			if v, err := strconv.Atoi(token.Value); err == nil {
				pq.MinPhysCores = v
			}
		case TokenMemory:
			// A bare size: host memory, or per-GPU memory in a GPU query.
			// Decided below, once the whole query is known (#236).
			if v, err := parseMemory(token.Value); err == nil {
				bareMemory, bareMemoryRaw = v, token.Raw
			}
		case TokenHostMemory:
			if v, err := parseMemory(token.Value); err == nil {
				pq.MinMemory = v
			}
		case TokenGPUMemory:
			if v, err := parseMemory(token.Value); err == nil {
				pq.MinGPUMemory = v
			}
		case TokenGPUCount:
			if v, err := strconv.Atoi(token.Value); err == nil {
				pq.GPUCount = v
			}
		case TokenArchitecture:
			pq.Architecture = token.Value
		case TokenNetworkSpeed:
			if v, err := parseNetworkSpeed(token.Value); err == nil {
				pq.MinNetworkGbps = v
			}
		case TokenEFA:
			pq.RequireEFA = true
		case TokenMIG:
			pq.RequireMIG = true
		case TokenNestedVirt:
			pq.RequireNestedV = true
		case TokenApp:
			pq.Apps = append(pq.Apps, token.Value)
		case TokenOr:
			pq.Operator = OperatorOr
		case TokenAnd:
			pq.Operator = OperatorAnd
		}
	}

	pq.assignBareMemory(bareMemory, bareMemoryRaw)

	// A query none of whose words constrain the search would search, and
	// price, the whole catalog (#229). If it has words we didn't recognise,
	// say so instead.
	if unknown := pq.UnknownTerms(); len(unknown) > 0 && !pq.hasConstraints() {
		return nil, newUnrecognizedQueryError(unknown)
	}

	// Validate
	if err := pq.Validate(); err != nil {
		return nil, err
	}

	return pq, nil
}

// assignBareMemory decides what a size with no "ram" or "vram" after it
// means (#236). In a query for a GPU or accelerator ("gpu 24gb", "h100
// 80gb") it is the memory of each GPU, since that is what a GPU's size
// usually refers to; elsewhere it is host memory, as before. If the query
// already gives per-GPU memory explicitly ("24gb vram 64gb") the bare size is
// host memory, and if it already gives host memory ("a10g 64gb ram 24gb")
// the bare size is per-GPU memory.
func (pq *ParsedQuery) assignBareMemory(v float64, raw string) {
	if v <= 0 {
		return
	}
	switch {
	case pq.MinGPUMemory == 0 && pq.wantsAccelerators():
		pq.MinGPUMemory = v
		pq.GPUMemoryRead = raw
	case pq.MinMemory == 0:
		pq.MinMemory = v
	}
}

// MemoryReadingNote is the one-line note for a query whose bare size was
// read as per-GPU memory, saying so and how to ask for host memory instead
// (#236). It is empty otherwise.
func (pq *ParsedQuery) MemoryReadingNote() string {
	if pq == nil || pq.GPUMemoryRead == "" {
		return ""
	}
	return fmt.Sprintf("Note: reading %q as memory per GPU (at least %s GB as sold; the VRAM column is in GiB); write \"%s ram\" for host memory",
		pq.GPUMemoryRead, strconv.FormatFloat(pq.MinGPUMemory, 'f', -1, 64), pq.GPUMemoryRead)
}

// UnknownTerms returns the query words the parser did not recognise, in
// query order.
func (pq *ParsedQuery) UnknownTerms() []string {
	if pq == nil {
		return nil
	}
	var out []string
	for _, t := range pq.RawTokens {
		if t.Type == TokenUnknown {
			out = append(out, t.Raw)
		}
	}
	return out
}

// hasConstraints reports whether the query narrows the search at all.
// Qualitative words, "and" and "or" only sort or combine.
func (pq *ParsedQuery) hasConstraints() bool {
	return len(pq.Vendors) > 0 || len(pq.Processors) > 0 || len(pq.GPUs) > 0 ||
		len(pq.Accelerators) > 0 || len(pq.InstructionSets) > 0 || len(pq.Sizes) > 0 || len(pq.Families) > 0 ||
		len(pq.InstanceSizes) > 0 || len(pq.Apps) > 0 || pq.MinVCPU > 0 ||
		pq.MinPhysCores > 0 || pq.MinMemory > 0 || pq.MinGPUMemory > 0 || pq.GPUCount > 0 ||
		pq.Architecture != "" || pq.MinNetworkGbps > 0 || pq.RequireEFA ||
		pq.RequireMIG || pq.RequireNestedV
}

func classifyTokens(words []string) []Token {
	var tokens []Token

	for i := 0; i < len(words); i++ {
		word := words[i]

		// Check multi-word patterns first, longest-match-first, so canonical keys
		// of any length resolve (e.g. "sapphire rapids", "radeon pro v520",
		// "rtx pro server 6000") rather than being shadowed by a single-token
		// alias or dropped (#90).
		if tok, consumed, ok := matchPhrase(words, i); ok {
			tokens = append(tokens, tok)
			i += consumed - 1
			continue
		}

		// Single-word patterns
		// Check app catalog first — app names take priority over hardware tokens.
		// Store the canonical name (entry.Name) so aliases resolve uniformly.
		if entry, ok := catalog.Lookup(word); ok {
			tokens = append(tokens, Token{Type: TokenApp, Value: entry.Name, Raw: word})
			continue
		}
		// Check vendor aliases before processor database
		if vendor, ok := metadata.VendorAliases[word]; ok {
			tokens = append(tokens, Token{Type: TokenVendor, Value: vendor, Raw: word})
		} else if _, ok := metadata.ProcessorDatabase[word]; ok {
			tokens = append(tokens, Token{Type: TokenProcessor, Value: word, Raw: word})
		} else if _, ok := metadata.GPUDatabase[word]; ok {
			tokens = append(tokens, Token{Type: TokenGPU, Value: word, Raw: word})
		} else if alias, ok := metadata.GPUAliases[word]; ok {
			tokens = append(tokens, Token{Type: TokenGPU, Value: alias, Raw: word})
		} else if _, ok := metadata.InstructionSetDatabase[word]; ok {
			tokens = append(tokens, Token{Type: TokenInstructionSet, Value: word, Raw: word})
		} else if alias, ok := metadata.InstructionSetAliases[word]; ok {
			tokens = append(tokens, Token{Type: TokenInstructionSet, Value: alias, Raw: word})
		} else if _, ok := metadata.SizeCategories[word]; ok {
			tokens = append(tokens, Token{Type: TokenSize, Value: word, Raw: word})
		} else if acc, ok := acceleratorWords[word]; ok {
			tokens = append(tokens, Token{Type: TokenAccelerator, Value: acc, Raw: word})
		} else if word == "efa" {
			tokens = append(tokens, Token{Type: TokenEFA, Value: "efa", Raw: word})
		} else if word == "mig" {
			tokens = append(tokens, Token{Type: TokenMIG, Value: "mig", Raw: word})
		} else if word == "and" {
			tokens = append(tokens, Token{Type: TokenAnd, Value: "and", Raw: word})
		} else if word == "or" {
			tokens = append(tokens, Token{Type: TokenOr, Value: "or", Raw: word})
		} else if word == "nested-virt" || word == "nested-virtualization" || word == "nestedvirt" {
			tokens = append(tokens, Token{Type: TokenNestedVirt, Value: "nested-virt", Raw: word})
		} else if alias, ok := metadata.NetworkAliases[word]; ok {
			if alias == "efa" {
				tokens = append(tokens, Token{Type: TokenEFA, Value: "efa", Raw: word})
			} else {
				// It's a bandwidth alias
				tokens = append(tokens, Token{Type: TokenNetworkSpeed, Value: alias, Raw: word})
			}
		} else if networkSpeedRegex.MatchString(word) {
			tokens = append(tokens, Token{Type: TokenNetworkSpeed, Value: word, Raw: word})
		} else if word == "x86_64" || word == "x86-64" || word == "x86" || word == "amd64" {
			tokens = append(tokens, Token{Type: TokenArchitecture, Value: "x86_64", Raw: word})
		} else if word == "arm64" || word == "arm" || word == "aarch64" {
			tokens = append(tokens, Token{Type: TokenArchitecture, Value: "arm64", Raw: word})
		} else if numberRegex.MatchString(word) {
			// Look ahead for units
			if i+1 < len(words) {
				next := words[i+1]
				// "8 physical cores" or "8 physical core"
				if next == "physical" && i+2 < len(words) && (words[i+2] == "cores" || words[i+2] == "core") {
					tokens = append(tokens, Token{Type: TokenPhysicalCores, Value: word, Raw: word + " physical cores"})
					i += 2
				} else if next == "cores" || next == "core" || next == "vcpus" || next == "vcpu" || next == "cpus" || next == "cpu" {
					tokens = append(tokens, Token{Type: TokenVCPU, Value: word, Raw: word + " " + next})
					i++
				} else if next == "gpus" || next == "gpu" {
					tokens = append(tokens, Token{Type: TokenGPUCount, Value: word, Raw: word + " " + next})
					i++
				} else if memoryRegex.MatchString(next) || strings.HasSuffix(next, "gb") || strings.HasSuffix(next, "gib") || strings.HasSuffix(next, "g") {
					tok, consumed := memoryToken(word+next, words, i+2)
					tokens = append(tokens, tok)
					i += 1 + consumed
				} else {
					tokens = append(tokens, Token{Type: TokenUnknown, Value: word, Raw: word})
				}
			} else {
				// A number with no unit after it says nothing; dropping it
				// silently hid that (#229).
				tokens = append(tokens, Token{Type: TokenUnknown, Value: word, Raw: word})
			}
		} else if memoryRegex.MatchString(word) {
			tok, consumed := memoryToken(word, words, i+1)
			tokens = append(tokens, tok)
			i += consumed
		} else if IsInstanceFamily(word) {
			// Checked before the hyphen split below, since family names such
			// as m7i-flex and p6-b200 contain hyphens (#229).
			tokens = append(tokens, Token{Type: TokenFamily, Value: word, Raw: word})
		} else if _, ok := metadata.SizeMultiplier(word); ok {
			// An instance size that isn't a size word ("xlarge", "2xlarge",
			// "metal-24xl"); size words such as "large" matched above (#229).
			tokens = append(tokens, Token{Type: TokenInstanceSize, Value: word, Raw: word})
		} else if isQualitativeKeyword(word) {
			tokens = append(tokens, Token{Type: TokenQualitative, Value: word, Raw: word})
		} else if subwords, ok := normalizeHyphenatedToken(word); ok {
			// Nothing above matched this word as a whole. Real GPU spec-strings
			// (Modal's documented convention, e.g. "RTX-PRO-6000", "A100-80GB",
			// "H100!", "B200+") hyphenate multi-word card names and append a
			// trailing !/+ suffix — but the tokenizer only splits on whitespace,
			// so these arrive as ONE token and no single- or multi-word lookup
			// above ever sees "rtx", "pro", "6000" as separate words (#130).
			// Re-run the classifier on the hyphen-split/suffix-stripped parts as
			// if they'd been space-separated all along, so e.g. "rtx-pro-6000"
			// resolves exactly like "rtx pro 6000" already does via matchPhrase's
			// alias lookup, and "a100-80gb" resolves like "a100 80gb" (GPU +
			// memory, two tokens) rather than needing a combined dictionary key.
			// subwords is always shorter/different from []string{word} (see
			// normalizeHyphenatedToken), so this cannot recurse forever.
			tokens = append(tokens, classifyTokens(subwords)...)
		} else {
			tokens = append(tokens, Token{Type: TokenUnknown, Value: word, Raw: word})
		}
	}

	return tokens
}

// memoryToken classifies the memory size by the words that follow it at
// words[next:] (#236): "ram", "memory", "mem", "host memory" or "host ram"
// mark host memory ([TokenHostMemory]); "vram", "gpu memory", "gpu mem",
// "gpu ram" or "per gpu" mark per-GPU memory ([TokenGPUMemory]). With none
// of these it is a bare [TokenMemory], whose meaning depends on the rest of
// the query (see assignBareMemory). It returns the token and how many of the
// following words it consumed.
func memoryToken(size string, words []string, next int) (Token, int) {
	at := func(k int) string {
		if next+k < len(words) {
			return words[next+k]
		}
		return ""
	}
	isMem := func(w string) bool { return w == "memory" || w == "mem" || w == "ram" }
	switch first, second := at(0), at(1); {
	case isMem(first):
		return Token{Type: TokenHostMemory, Value: size, Raw: size + " " + first}, 1
	case first == "host" && isMem(second):
		return Token{Type: TokenHostMemory, Value: size, Raw: size + " host " + second}, 2
	case first == "vram":
		return Token{Type: TokenGPUMemory, Value: size, Raw: size + " vram"}, 1
	case (first == "gpu" && isMem(second)) || (first == "per" && second == "gpu"):
		return Token{Type: TokenGPUMemory, Value: size, Raw: size + " " + first + " " + second}, 2
	}
	return Token{Type: TokenMemory, Value: size, Raw: size}, 0
}

// acceleratorWords maps the generic accelerator words to a
// [ParsedQuery.Accelerators] value (#233). "gpu", "gpus", "accelerator" and
// "accelerators" mean any GPU or accelerator family (metadata's
// acceleratorFamilies: GPUs, Inferentia, Trainium, Qualcomm AI 100, FPGAs
// and media accelerators). "training" and "inference" mean the GPUs and accelerators
// GPUDatabase files under that use case: training is H100, H200, A100, V100,
// B200, B300, Trainium and Trainium2; inference is A10G, T4, L4, L40S,
// Inferentia and Inferentia2. That is how AWS positions the chips, not a
// limit on what each can run, so "training" leaves out L40S and "inference"
// leaves out H100; name the GPU to get those.
var acceleratorWords = map[string]string{
	"gpu":          acceleratorAny,
	"gpus":         acceleratorAny,
	"accelerator":  acceleratorAny,
	"accelerators": acceleratorAny,
	"training":     "training",
	"inference":    "inference",
}

// acceleratorAny is the [ParsedQuery.Accelerators] value for any GPU or
// accelerator.
const acceleratorAny = "any"

// IsRecognizedTerm reports whether a single space-free word is fully
// classified by the tokenizer — i.e. resolves entirely to known token types,
// with no leftover [TokenUnknown] part — after lowercasing and, if needed,
// the same hyphen-split/suffix-strip normalization [ParseQuery] itself applies
// (#130). It exists for callers upstream of ParseQuery that need to decide
// whether a bare word is recognized vocabulary before choosing which pipeline
// to hand it to — cmd's looksLikePattern uses it to avoid routing a real GPU
// spec-string like "RTX-PRO-6000" or "a100-80gb" to the literal instance-type
// pattern matcher just because it contains a hyphen or ends in a digit.
func IsRecognizedTerm(word string) bool {
	lower := strings.ToLower(strings.TrimSpace(word))
	if lower == "" {
		return false
	}
	toks := classifyTokens([]string{lower})
	if len(toks) == 0 {
		// A bare number with no unit lookahead (e.g. "8" with nothing after
		// it) classifies to zero tokens rather than TokenUnknown — that is
		// "nothing recognized," not "everything recognized."
		return false
	}
	for _, t := range toks {
		// A bare family name ("c6i", "m7i-flex") is left to the caller's
		// pattern path, which matches the whole family (#229).
		if t.Type == TokenUnknown || t.Type == TokenFamily {
			return false
		}
	}
	return true
}

// trailingSuffixRegex strips a Modal-documented card-spec suffix: "!" pins to
// the named generation and opts out of auto-upgrade (e.g. "H100!"); "+" opts
// in to a dual-generation match (e.g. "B200+"). Neither carries lexical
// meaning for truffle's own vocabulary (no GPU/processor/instruction-set key
// ends in one), so stripping it before classification is safe — it's dropped
// rather than mapped to a token, since truffle has no generation-pinning
// concept to route it to.
var trailingSuffixRegex = regexp.MustCompile(`[!+]$`)

// normalizeHyphenatedToken reports whether word — having already failed every
// other single-word classification — is a hyphenated and/or suffixed spec
// string that would classify successfully if split back into the
// space-separated words it represents (#130). Returns the candidate subwords
// and true only when that split could plausibly help: word must actually
// contain a hyphen or a trailing !/+ (otherwise there's nothing to split, and
// returning ok for an ordinary unknown word would recurse into itself forever
// via classifyTokens).
//
// This intentionally does not check whether the subwords classify successfully
// — it only decides whether re-tokenizing is worth attempting. A hyphenated
// word that still doesn't resolve after splitting correctly falls through to
// TokenUnknown for each part, same as typing those parts space-separated in
// the first place.
func normalizeHyphenatedToken(word string) ([]string, bool) {
	stripped := trailingSuffixRegex.ReplaceAllString(word, "")
	if !strings.Contains(stripped, "-") {
		if stripped == word {
			return nil, false // no hyphen, no suffix — nothing to normalize
		}
		// A bare suffix with no hyphen (e.g. "h100!"): one resulting subword,
		// guaranteed different from the original (the suffix is gone), so no
		// infinite recursion risk.
		return []string{stripped}, true
	}
	return strings.Split(stripped, "-"), true
}

// parseMemory parses memory string (e.g., "32gb", "64gib") to GiB
func parseMemory(s string) (float64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	matches := memoryRegex.FindStringSubmatch(s)
	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid memory format: %s", s)
	}

	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}

	// All units are treated as GiB
	return value, nil
}

var qualitativeKeywords = map[string]bool{
	"cheap": true, "cheapest": true, "affordable": true, "budget": true,
	"fast": true, "fastest": true, "quick": true,
	"slow": true, "slowest": true,
	"expensive": true, "premium": true,
	"best": true, "worst": true, "optimal": true,
	"powerful": true, "performant": true,
	"efficient": true, "inefficient": true,
	"popular": true, "recommended": true,
	"new": true, "newest": true, "latest": true,
	"old": true, "oldest": true, "legacy": true,
}

// SortPreference represents a user-requested sort derived from qualitative keywords.
type SortPreference int

const (
	SortDefault    SortPreference = iota
	SortCheapest                  // sort by on-demand price ascending
	SortExpensive                 // sort by on-demand price descending
	SortNewest                    // sort by generation descending
	SortPerformant                // sort by vCPU count descending
)

var QualitativeSortMap = map[string]SortPreference{
	"cheap": SortCheapest, "cheapest": SortCheapest, "affordable": SortCheapest, "budget": SortCheapest,
	"expensive": SortExpensive, "premium": SortExpensive,
	"fast": SortPerformant, "fastest": SortPerformant, "powerful": SortPerformant, "performant": SortPerformant,
	"new": SortNewest, "newest": SortNewest, "latest": SortNewest,
}

// SortPreference returns the sort preference derived from qualitative keywords in the query.
func (pq *ParsedQuery) SortPreference() SortPreference {
	for _, t := range pq.RawTokens {
		if t.Type == TokenQualitative {
			if pref, ok := QualitativeSortMap[t.Value]; ok {
				return pref
			}
		}
	}
	return SortDefault
}

func isQualitativeKeyword(word string) bool {
	return qualitativeKeywords[word]
}

// QualitativeTokens returns any qualitative keywords found in the parsed query.
func (pq *ParsedQuery) QualitativeTokens() []string {
	var quals []string
	for _, t := range pq.RawTokens {
		if t.Type == TokenQualitative {
			quals = append(quals, t.Raw)
		}
	}
	return quals
}

// parseNetworkSpeed parses network speed string (e.g., "10gbps", "100g") to Gbps
func parseNetworkSpeed(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	// Check if it's a known alias (e.g., "10gbps", "25gbps")
	if _, ok := metadata.NetworkBandwidthTiers[s]; ok {
		// Extract the number from the key (e.g., "10gbps" -> 10)
		matches := regexp.MustCompile(`^(\d+)`).FindStringSubmatch(s)
		if len(matches) == 2 {
			if v, err := strconv.Atoi(matches[1]); err == nil {
				return v, nil
			}
		}
	}

	// Try parsing as "100gbps" or "100g"
	matches := networkSpeedRegex.FindStringSubmatch(s)
	if len(matches) == 3 {
		value, err := strconv.Atoi(matches[1])
		if err != nil {
			return 0, err
		}
		return value, nil
	}

	return 0, fmt.Errorf("invalid network speed format: %s", s)
}

// Validate checks for conflicting or invalid query criteria
func (pq *ParsedQuery) Validate() error {
	// Check for conflicting architectures
	archSet := make(map[string]bool)

	// From processors
	for _, proc := range pq.Processors {
		if info, ok := metadata.ProcessorDatabase[proc]; ok {
			archSet[info.Architecture] = true
		}
	}

	// From vendors
	for _, vendor := range pq.Vendors {
		for _, info := range metadata.ProcessorDatabase {
			if info.Vendor == vendor {
				archSet[info.Architecture] = true
			}
		}
	}

	// From instruction sets
	for _, is := range pq.InstructionSets {
		if info, ok := metadata.InstructionSetDatabase[is]; ok {
			archSet[info.Architecture] = true
		}
	}

	// From explicit architecture
	if pq.Architecture != "" {
		archSet[pq.Architecture] = true
	}

	if len(archSet) > 1 {
		archs := make([]string, 0, len(archSet))
		for arch := range archSet {
			archs = append(archs, arch)
		}
		return fmt.Errorf("conflicting architectures: %v", archs)
	}

	return nil
}

// familySet builds a map[string]bool from a family slice, for use as one
// input to intersectAll/unionAll.
func familySet(families []string) map[string]bool {
	set := make(map[string]bool, len(families))
	for _, f := range families {
		set[f] = true
	}
	return set
}

// intersectAll returns the intersection of all given sets. An empty input
// slice returns an empty (non-nil) set, matching the "no active dimensions"
// case, which must NOT be treated as "everything".
func intersectAll(sets []map[string]bool) map[string]bool {
	if len(sets) == 0 {
		return map[string]bool{}
	}
	result := make(map[string]bool)
	for k := range sets[0] {
		result[k] = true
	}
	for _, s := range sets[1:] {
		for k := range result {
			if !s[k] {
				delete(result, k)
			}
		}
	}
	return result
}

// unionAll returns the union of all given sets.
func unionAll(sets []map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for _, s := range sets {
		for k := range s {
			result[k] = true
		}
	}
	return result
}

// ResolveInstanceFamilies returns all instance families matching the query.
//
// Each query dimension (vendor, processor, GPU, instruction set, EFA, MIG,
// network speed) is resolved to its own family set; within a dimension,
// multiple values always OR together (e.g. "a100 h100" means A100-or-H100,
// since no instance can be both). Only dimensions the user actually
// specified participate in the cross-dimension combination step, which
// defaults to AND (intersection) and switches to OR (union) when the query
// contains an explicit "or" (#144).
func (pq *ParsedQuery) ResolveInstanceFamilies() []string {
	var dimensions []map[string]bool

	// CPU words (vendor, processor, instruction set) select CPU-only families
	// unless the query also asks for a GPU or accelerator (#228): "amd 16
	// cores 64gb" means an AMD CPU instance, not g5.4xlarge, while "amd a10g"
	// still finds g5 by its AMD host CPU.
	cpuOnly := !pq.wantsAccelerators()
	addCPUFamily := func(set map[string]bool, family string) {
		if !cpuOnly || !metadata.IsAcceleratorFamily(family) {
			set[family] = true
		}
	}

	if len(pq.Processors) > 0 {
		set := make(map[string]bool)
		for _, proc := range pq.Processors {
			if info, ok := metadata.ProcessorDatabase[proc]; ok {
				for _, family := range info.Families {
					addCPUFamily(set, family)
				}
			}
		}
		dimensions = append(dimensions, set)
	}

	if len(pq.Vendors) > 0 {
		set := make(map[string]bool)
		for _, vendor := range pq.Vendors {
			for _, family := range metadata.GetFamiliesByVendor(vendor) {
				addCPUFamily(set, family)
			}
		}
		dimensions = append(dimensions, set)
	}

	if len(pq.GPUs) > 0 {
		set := make(map[string]bool)
		for _, gpu := range pq.GPUs {
			if info, ok := metadata.GPUDatabase[gpu]; ok {
				for _, family := range info.Families {
					set[family] = true
				}
			}
		}
		dimensions = append(dimensions, set)
	}

	if len(pq.Accelerators) > 0 {
		dimensions = append(dimensions, familySet(pq.acceleratorFamilies()))
	}

	if len(pq.InstructionSets) > 0 {
		set := make(map[string]bool)
		for _, is := range pq.InstructionSets {
			for _, family := range metadata.GetFamiliesByInstructionSet(is) {
				addCPUFamily(set, family)
			}
		}
		dimensions = append(dimensions, set)
	}

	if len(pq.Families) > 0 {
		dimensions = append(dimensions, familySet(pq.Families))
	}

	if pq.RequireEFA {
		dimensions = append(dimensions, familySet(metadata.GetFamiliesByEFA()))
	}

	if pq.RequireMIG {
		dimensions = append(dimensions, familySet(metadata.GetMIGCapableFamilies()))
	}

	if pq.MinNetworkGbps > 0 {
		dimensions = append(dimensions, familySet(metadata.GetFamiliesByNetworkSpeed(pq.MinNetworkGbps)))
	}

	var queryResult map[string]bool
	if pq.Operator == OperatorOr {
		queryResult = unionAll(dimensions)
	} else {
		queryResult = intersectAll(dimensions)
	}

	// Collect families from app catalog entries
	appFamilies := make(map[string]bool)
	for _, appName := range pq.Apps {
		if entry, ok := catalog.Lookup(appName); ok {
			for _, family := range entry.InstanceFamilies {
				appFamilies[family] = true
			}
		}
	}

	// If both app families and query dimensions are present, intersect them.
	// This ensures "graviton --app paraview" only returns families that satisfy
	// BOTH constraints, not the union of all. App-vs-query is always an
	// intersection regardless of pq.Operator — the operator governs
	// combination among the query's own dimensions only.
	var result map[string]bool
	if len(appFamilies) > 0 && len(dimensions) > 0 {
		result = make(map[string]bool)
		for family := range appFamilies {
			if queryResult[family] {
				result[family] = true
			}
		}
	} else if len(appFamilies) > 0 {
		result = appFamilies
	} else {
		result = queryResult
	}

	families := make([]string, 0, len(result))
	for family := range result {
		families = append(families, family)
	}

	return families
}

// acceleratorFamilies returns the families the query's accelerator words
// select: the families of its use cases ("training", "inference"), or every
// accelerator family when it names only "gpu"/"accelerator". A use case
// narrows the generic word, so "inference gpu" means inference GPUs (#233).
func (pq *ParsedQuery) acceleratorFamilies() []string {
	var families []string
	for _, acc := range pq.Accelerators {
		if acc != acceleratorAny {
			families = append(families, metadata.GetFamiliesByUseCase(acc)...)
		}
	}
	if len(families) == 0 {
		return metadata.AcceleratorFamilies()
	}
	return families
}

// wantsAccelerators reports whether the query asks for a GPU or accelerator:
// a GPU or accelerator name (a10g, h100, nvidia, inferentia, trainium, ...),
// an accelerator word (gpu, accelerator, training, inference), a GPU count,
// MIG, per-GPU memory ("24gb vram"), or an accelerator family named directly
// (g5, p5).
func (pq *ParsedQuery) wantsAccelerators() bool {
	if len(pq.GPUs) > 0 || len(pq.Accelerators) > 0 || pq.GPUCount > 0 || pq.RequireMIG || pq.MinGPUMemory > 0 {
		return true
	}
	for _, f := range pq.Families {
		if metadata.IsAcceleratorFamily(f) {
			return true
		}
	}
	return false
}

// hasFamilyDimensions reports whether any query dimension that resolves to a
// family set is present.
func (pq *ParsedQuery) hasFamilyDimensions() bool {
	return len(pq.GPUs) > 0 || pq.hasOtherActiveDimensions()
}

// HasNoMatchingFamily reports whether the query names family constraints
// (vendor, processor, GPU, family, ...) that no instance family satisfies
// together, e.g. "intel h100": every H100 host has an AMD CPU (#229). Such a
// query matches nothing.
func (pq *ParsedQuery) HasNoMatchingFamily() bool {
	return (pq.hasFamilyDimensions() || len(pq.Apps) > 0) && len(pq.ResolveInstanceFamilies()) == 0
}

// hasOtherActiveDimensions reports whether any family-contributing dimension
// OTHER than GPUs is present in the query (vendor, processor, accelerator
// word, instruction set, EFA, MIG, network speed). Used by buildInstanceTypePattern to decide
// whether the GPU-exact-instance-type shortcut is safe to take (#144) — it
// isn't, once another dimension also needs to constrain the result.
func (pq *ParsedQuery) hasOtherActiveDimensions() bool {
	return len(pq.Vendors) > 0 || len(pq.Processors) > 0 || len(pq.Accelerators) > 0 || len(pq.InstructionSets) > 0 || len(pq.Families) > 0 || pq.RequireEFA || pq.RequireMIG || pq.MinNetworkGbps > 0
}

// ResolveGPUInstances returns exact instance types for GPU queries
func (pq *ParsedQuery) ResolveGPUInstances() []string {
	instanceSet := make(map[string]bool)

	for _, gpu := range pq.GPUs {
		if info, ok := metadata.GPUDatabase[gpu]; ok {
			for _, inst := range info.InstanceTypes {
				instanceSet[inst] = true
			}
		}
	}

	instances := make([]string, 0, len(instanceSet))
	for inst := range instanceSet {
		instances = append(instances, inst)
	}

	return instances
}

// DeriveArchitecture determines the architecture from query criteria
func (pq *ParsedQuery) DeriveArchitecture() string {
	if pq.Architecture != "" {
		return pq.Architecture
	}

	archSet := make(map[string]bool)

	// From processors
	for _, proc := range pq.Processors {
		if info, ok := metadata.ProcessorDatabase[proc]; ok {
			archSet[info.Architecture] = true
		}
	}

	// From vendors
	for _, vendor := range pq.Vendors {
		for _, info := range metadata.ProcessorDatabase {
			if info.Vendor == vendor {
				archSet[info.Architecture] = true
			}
		}
	}

	// From instruction sets
	for _, is := range pq.InstructionSets {
		if info, ok := metadata.InstructionSetDatabase[is]; ok {
			archSet[info.Architecture] = true
		}
	}

	// Return architecture only if unambiguous
	if len(archSet) == 1 {
		for arch := range archSet {
			return arch
		}
	}

	return ""
}

// BuildSizePattern returns a regex pattern for size filtering: the union of
// the query's size categories, each derived from its size range (#220), so
// it matches numbered sizes such as 3xlarge or metal-24xl as well as named
// ones.
func (pq *ParsedQuery) BuildSizePattern() string {
	seen := make(map[string]bool)
	var patterns []string
	for _, sizeCategory := range pq.Sizes {
		if p := metadata.SizePattern(sizeCategory); p != "" && !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}
	for _, size := range pq.InstanceSizes {
		if p := regexp.QuoteMeta(size); !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}
	if len(patterns) == 0 {
		return ".*"
	}
	sort.Strings(patterns)
	return "(" + strings.Join(patterns, "|") + ")"
}

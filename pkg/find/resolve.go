package find

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/metadata"
)

// ErrNoMatch is returned by [ResolveCard] when a card name does not resolve to
// any known GPU / instance types. It exists so a card-name consumer gets an
// explicit "no match" instead of the free-text search pipeline's match-all
// (".*") fallback, which silently degrades an unresolved query into "every
// instance type" — a dangerous result to mistake for a real one (#90).
var ErrNoMatch = errors.New("truffle: no instance types match card")

// ResolveCard maps a GPU card name (e.g. "RTX PRO 6000", "H100", "L40S") to the
// concrete EC2 instance types that carry it. It is the strict, card-oriented
// counterpart to the free-text [ParseQuery]/[ParsedQuery.BuildCriteria] pipeline:
// a card that resolves to nothing returns [ErrNoMatch], never a match-all
// pattern. The returned instance types are sorted for stable output.
//
// It resolves via the same catalog and alias table as the search pipeline, so
// marketing spellings ("rtx pro 6000") and canonical names ("rtx pro server
// 6000") both work.
//
// A memory size in the card name is the memory of each GPU, as in Modal's
// "A100-80GB": it names a variant of the card, so it is applied (#245).
// "A100-80GB" resolves to p4de.24xlarge only, not to the 40 GB p4d.24xlarge as
// well, which would place an 80 GB job on GPUs too small for it. The size is a
// minimum, as everywhere in truffle (MinGPUMemory, #236), so "A100-40GB" keeps
// both. A family whose per-GPU memory isn't known is dropped rather than
// assumed to fit, and if nothing is left the card is [ErrNoMatch].
//
// Other constraints in the string (vCPUs, size, …) are NOT applied: this is a
// card resolver, not a search, so use ParseQuery for compound queries. Whether
// they should be rejected instead, as this comment once claimed they were, is
// #246.
func ResolveCard(card string) ([]string, error) {
	pq, err := ParseQuery(card)
	if uq := (*UnrecognizedQueryError)(nil); errors.As(err, &uq) {
		return nil, fmt.Errorf("%w: %q resolved to no GPU (unknown: %s)", ErrNoMatch, card, strings.Join(uq.Terms, ", "))
	}
	if err != nil {
		return nil, err // empty / unparseable input
	}
	if pq.IsPattern() {
		return nil, fmt.Errorf("%w: %q reads as an instance-type pattern, not a GPU card", ErrNoMatch, card)
	}
	if len(pq.GPUs) == 0 {
		return nil, fmt.Errorf("%w: %q resolved to no GPU (tokens: %s)", ErrNoMatch, card, tokenSummary(pq))
	}

	instances := pq.ResolveGPUInstances()
	if len(instances) == 0 {
		return nil, fmt.Errorf("%w: %q (no instance types for GPU %v)", ErrNoMatch, card, pq.GPUs)
	}
	if pq.MinGPUMemory > 0 {
		instances = withGPUMemory(instances, pq.GPUs, pq.MinGPUMemory, pq.ExactMatch)
		if len(instances) == 0 {
			return nil, fmt.Errorf("%w: %q (no instance type for GPU %v has %v GB/GiB per GPU, as sold)", ErrNoMatch, card, pq.GPUs, pq.MinGPUMemory)
		}
	}

	sort.Strings(instances)
	return instances, nil
}

// tokenSummary renders the classified tokens of a parsed card query for a
// legible ErrNoMatch message — so a caller can see what truffle did/didn't
// recognize in their input.
func tokenSummary(pq *ParsedQuery) string {
	if len(pq.RawTokens) == 0 {
		return "none recognized"
	}
	parts := make([]string, 0, len(pq.RawTokens))
	for _, t := range pq.RawTokens {
		if t.Type == TokenUnknown {
			parts = append(parts, t.Raw+"=?")
			continue
		}
		parts = append(parts, t.Raw+"="+t.Value)
	}
	return strings.Join(parts, " ")
}

// CardInstanceTypes is the metadata-only sibling of [ResolveCard]: it looks a
// card up directly in the catalog (including aliases) without running the token
// parser, returning the canonical GPU key and its instance types. It is useful
// when a caller already holds a clean card key and wants an exact catalog hit
// rather than free-text tolerance. Returns [ErrNoMatch] if the card is unknown.
func CardInstanceTypes(card string) (canonical string, instances []string, err error) {
	key := strings.ToLower(strings.TrimSpace(card))
	if key == "" {
		return "", nil, fmt.Errorf("%w: empty card", ErrNoMatch)
	}
	if alias, ok := metadata.GPUAliases[key]; ok {
		key = alias
	}
	info, ok := metadata.GPUDatabase[key]
	if !ok {
		return "", nil, fmt.Errorf("%w: %q", ErrNoMatch, card)
	}
	out := append([]string(nil), info.InstanceTypes...)
	sort.Strings(out)
	return key, out, nil
}

// withGPUMemory keeps the instance types whose GPUs have at least wantGB each
// (exactly, when exact), compared as aws.MatchesGPUMemory compares them.
func withGPUMemory(instances, gpus []string, wantGB float64, exact bool) []string {
	var out []string
	for _, inst := range instances {
		if mib, ok := instanceGPUMemoryMiB(inst, gpus); ok && aws.MatchesGPUMemory(mib, wantGB, exact) {
			out = append(out, inst)
		}
	}
	return out
}

// instanceGPUMemoryMiB is the memory of one of inst's GPUs: its family's
// recorded figure (metadata.AcceleratorMemoryMiB, which tells a p4de A100's
// 80 GB from a p4d's 40), else the GPU model's MemoryGB, as that table's
// comment directs for families it leaves out. A fractional-GPU family has
// neither: its instances get a partition smaller than the model's memory and
// sized by instance type, so it is unknown here and the type is dropped.
func instanceGPUMemoryMiB(inst string, gpus []string) (int64, bool) {
	family := extractFamily(inst)
	if mib, ok := metadata.AcceleratorMemoryMiB(family); ok {
		return mib, true
	}
	if metadata.IsFractionalGPUFamily(family) {
		return 0, false
	}
	for _, g := range gpus {
		if info, ok := metadata.GPUDatabase[g]; ok && info.MemoryGB > 0 && containsString(info.InstanceTypes, inst) {
			return int64(info.MemoryGB) * 1024, true
		}
	}
	return 0, false
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

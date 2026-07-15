package find

import (
	"errors"
	"fmt"
	"sort"
	"strings"

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
// 6000") both work. A query that carries constraints other than the card
// (vCPUs, memory, size, …) is rejected — this is a card resolver, not a search;
// use ParseQuery for compound queries.
func ResolveCard(card string) ([]string, error) {
	pq, err := ParseQuery(card)
	if err != nil {
		return nil, err // empty / unparseable input
	}
	if len(pq.GPUs) == 0 {
		return nil, fmt.Errorf("%w: %q resolved to no GPU (tokens: %s)", ErrNoMatch, card, tokenSummary(pq))
	}

	instances := pq.ResolveGPUInstances()
	if len(instances) == 0 {
		return nil, fmt.Errorf("%w: %q (no instance types for GPU %v)", ErrNoMatch, card, pq.GPUs)
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

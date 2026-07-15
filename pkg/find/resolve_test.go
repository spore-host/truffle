package find

import (
	"errors"
	"testing"
)

// TestParseQuery_MultiWordGPU covers the #90 structural fix: multi-word GPU card
// names now resolve longest-match-first (up to the longest catalog key), and the
// alias table is consulted in the multi-word path — so both the canonical
// "rtx pro server 6000" and the marketing spelling "rtx pro 6000" reach g7e,
// instead of only the single-token "rtx" alias catching it while "pro"/"6000"
// were dropped.
func TestParseQuery_MultiWordGPU(t *testing.T) {
	tests := []struct {
		query    string
		wantGPUs []string
	}{
		{"rtx pro 6000", []string{"rtx pro server 6000"}},        // marketing spelling → alias
		{"RTX PRO 6000", []string{"rtx pro server 6000"}},        // case-insensitive
		{"rtx pro server 6000", []string{"rtx pro server 6000"}}, // 4-word canonical key (was unreachable)
		{"radeon pro v520", []string{"radeon pro v520"}},         // pre-existing 3-word key still works
		{"rtx", []string{"rtx pro server 6000"}},                 // single-token alias still works
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", tt.query, err)
			}
			if !stringSlicesEqual(pq.GPUs, tt.wantGPUs) {
				t.Errorf("GPUs = %v, want %v", pq.GPUs, tt.wantGPUs)
			}
			// No leftover TokenUnknown from "pro"/"6000" being dropped.
			for _, tok := range pq.RawTokens {
				if tok.Type == TokenUnknown {
					t.Errorf("unexpected unknown token %q in %q — phrase should consume it", tok.Raw, tt.query)
				}
			}
		})
	}
}

// TestResolveCard_Resolves verifies the strict card resolver returns concrete,
// sorted instance types for a known card via both canonical and marketing names.
func TestResolveCard_Resolves(t *testing.T) {
	want := []string{
		"g7e.12xlarge", "g7e.24xlarge", "g7e.2xlarge",
		"g7e.48xlarge", "g7e.4xlarge", "g7e.8xlarge",
	} // sorted lexically
	for _, card := range []string{"RTX PRO 6000", "rtx pro server 6000", "rtx"} {
		got, err := ResolveCard(card)
		if err != nil {
			t.Fatalf("ResolveCard(%q): unexpected error %v", card, err)
		}
		if !stringSlicesEqual(got, want) {
			t.Errorf("ResolveCard(%q) = %v, want %v", card, got, want)
		}
	}
}

// TestResolveCard_NoMatch verifies the whole point of the resolver (#90): an
// unresolved card returns ErrNoMatch, NOT a silent match-all. This is the
// footgun the free-text ".*" fallback creates for a card consumer.
func TestResolveCard_NoMatch(t *testing.T) {
	for _, card := range []string{"totally unknown card", "geforce 9000", "16gb"} {
		got, err := ResolveCard(card)
		if !errors.Is(err, ErrNoMatch) {
			t.Errorf("ResolveCard(%q): err = %v, want ErrNoMatch; got instances %v", card, err, got)
		}
		if got != nil {
			t.Errorf("ResolveCard(%q): want nil instances on no-match, got %v", card, got)
		}
	}
}

// TestResolveCard_EmptyInput returns an error (not a panic, not match-all).
func TestResolveCard_EmptyInput(t *testing.T) {
	if _, err := ResolveCard(""); err == nil {
		t.Error("ResolveCard(\"\"): want error, got nil")
	}
}

// TestCardInstanceTypes verifies the metadata-only lookup resolves aliases to the
// canonical key + sorted instance types, and reports ErrNoMatch for unknowns.
func TestCardInstanceTypes(t *testing.T) {
	canonical, insts, err := CardInstanceTypes("rtx pro 6000")
	if err != nil {
		t.Fatalf("CardInstanceTypes: %v", err)
	}
	if canonical != "rtx pro server 6000" {
		t.Errorf("canonical = %q, want rtx pro server 6000", canonical)
	}
	if len(insts) != 6 || insts[0] != "g7e.12xlarge" {
		t.Errorf("instances = %v, want 6 sorted g7e types", insts)
	}

	if _, _, err := CardInstanceTypes("no such gpu"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("CardInstanceTypes(unknown): err = %v, want ErrNoMatch", err)
	}
}

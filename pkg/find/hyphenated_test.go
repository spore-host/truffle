package find

import "testing"

// TestResolveCard_HyphenatedSpecStrings is the regression test for #130:
// Modal's documented GPU spec-string convention (modal.com/docs) hyphenates
// multi-word card names and appends a trailing "!"/"+" suffix
// (gpu="RTX-PRO-6000", gpu="A100-80GB", gpu="H100!", gpu="B200+"). The
// tokenizer split only on whitespace, so these arrived as one token each and
// never matched any single- or multi-word vocabulary entry — confirmed live
// via ResolveCard before this fix: each returned ErrNoMatch with the whole
// hyphenated string as an unrecognized token.
//
// This isn't a narrow RTX-PRO case: any hyphenated or "!"/"+"-suffixed GPU
// term fails identically, since the root cause (whitespace-only tokenization)
// applies uniformly. The table below covers both the original report and the
// follow-up comment's broader confirmation.
func TestResolveCard_HyphenatedSpecStrings(t *testing.T) {
	tests := []struct {
		hyphenated string
		spaced     string // the equivalent, already-working space-separated form
	}{
		{"RTX-PRO-6000", "RTX PRO 6000"},
		{"rtx-pro-6000", "rtx pro 6000"},
		{"RTX-PRO-4500", "RTX PRO 4500"},
		{"A100-80GB", "A100 80GB"}, // GPU + memory constraint, two distinct tokens
		{"H100!", "H100"},          // trailing "!" (pin, no auto-upgrade) has no truffle equivalent — just stripped
		{"B200+", "B200"},          // trailing "+" (opt-in dual-gen) same treatment
	}
	for _, tt := range tests {
		t.Run(tt.hyphenated, func(t *testing.T) {
			gotHyphenated, errH := ParseQuery(tt.hyphenated)
			if errH != nil {
				t.Fatalf("ParseQuery(%q): %v", tt.hyphenated, errH)
			}
			gotSpaced, errS := ParseQuery(tt.spaced)
			if errS != nil {
				t.Fatalf("ParseQuery(%q): %v", tt.spaced, errS)
			}
			if len(gotHyphenated.GPUs) == 0 {
				t.Fatalf("ParseQuery(%q).GPUs is empty, want a match (same as %q)", tt.hyphenated, tt.spaced)
			}
			if !stringSlicesEqual(gotHyphenated.GPUs, gotSpaced.GPUs) {
				t.Errorf("ParseQuery(%q).GPUs = %v, want the same as ParseQuery(%q).GPUs = %v",
					tt.hyphenated, gotHyphenated.GPUs, tt.spaced, gotSpaced.GPUs)
			}

			// The resolved instance types must also match — this is what a
			// caller actually consumes.
			instH := gotHyphenated.ResolveGPUInstances()
			instS := gotSpaced.ResolveGPUInstances()
			if len(instH) == 0 {
				t.Errorf("ResolveGPUInstances() for %q is empty", tt.hyphenated)
			}
			if !stringSlicesEqualUnordered(instH, instS) {
				t.Errorf("ResolveGPUInstances() for %q = %v, want the same set as %q = %v",
					tt.hyphenated, instH, tt.spaced, instS)
			}
		})
	}
}

// TestResolveCard_HyphenatedFormResolves drives the full ResolveCard entry
// point (rather than ParseQuery directly) for the exact strings named in
// #130's report and its follow-up comment, confirming each now returns
// concrete instance types instead of ErrNoMatch.
func TestResolveCard_HyphenatedFormResolves(t *testing.T) {
	for _, card := range []string{"RTX-PRO-6000", "RTX-PRO-4500", "A100-80GB", "H100!", "B200+"} {
		t.Run(card, func(t *testing.T) {
			got, err := ResolveCard(card)
			if err != nil {
				t.Fatalf("ResolveCard(%q): unexpected error %v", card, err)
			}
			if len(got) == 0 {
				t.Errorf("ResolveCard(%q) returned no instance types", card)
			}
		})
	}
}

// TestNormalizeHyphenatedToken exercises the splitting helper directly,
// including the guard against infinite recursion (an ordinary word with no
// hyphen or suffix must report ok=false, not a same-word "split").
func TestNormalizeHyphenatedToken(t *testing.T) {
	tests := []struct {
		word string
		want []string
		ok   bool
	}{
		{"rtx-pro-6000", []string{"rtx", "pro", "6000"}, true},
		{"a100-80gb", []string{"a100", "80gb"}, true},
		{"h100!", []string{"h100"}, true},
		{"b200+", []string{"b200"}, true},
		{"plainword", nil, false},
		{"m7i", nil, false}, // no hyphen, no suffix — must not "split" into itself
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			got, ok := normalizeHyphenatedToken(tt.word)
			if ok != tt.ok {
				t.Fatalf("normalizeHyphenatedToken(%q) ok = %v, want %v", tt.word, ok, tt.ok)
			}
			if ok && !stringSlicesEqual(got, tt.want) {
				t.Errorf("normalizeHyphenatedToken(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}

// TestClassifyTokens_HyphenatedNoInfiniteRecursion is a direct guard on the
// recursion safety normalizeHyphenatedToken's doc comment claims: an unknown
// word containing a hyphen whose parts are ALSO unrecognized must terminate
// with TokenUnknown parts, not loop. go test's own deadline would catch a
// real infinite loop, but asserting the terminal shape makes the guarantee
// explicit rather than incidental.
func TestClassifyTokens_HyphenatedNoInfiniteRecursion(t *testing.T) {
	toks := classifyTokens([]string{"foo-bar-baz"})
	if len(toks) != 3 {
		t.Fatalf("classifyTokens(%q) = %d tokens, want 3 (one per hyphen-split part)", "foo-bar-baz", len(toks))
	}
	for _, tok := range toks {
		if tok.Type != TokenUnknown {
			t.Errorf("classifyTokens(%q): part %q classified as %v, want TokenUnknown (no such vocabulary)", "foo-bar-baz", tok.Raw, tok.Type)
		}
	}
}

// TestIsRecognizedTerm covers the exported helper cmd's looksLikePattern
// depends on to decide whether a bare word is real vocabulary before routing
// it to the natural-language parser vs. the literal instance-type pattern
// matcher (#130). Case-insensitivity matters here specifically: the CLI
// routing bug this was added to fix only manifested for uppercase/mixed-case
// input, since the underlying tables are lowercase-keyed.
func TestIsRecognizedTerm(t *testing.T) {
	tests := []struct {
		word string
		want bool
	}{
		{"H100", true},
		{"h100", true},
		{"AVX2", true}, // uppercase: this exact case silently failed before IsRecognizedTerm existed
		{"avx2", true},
		{"A100-80GB", true},
		{"a100-80gb", true},
		{"RTX-PRO-6000", true},
		{"B200+", true},
		{"h100!", true},
		{"m7i", false},        // a real instance family, but not GPU/processor/instruction-set vocabulary
		{"foo-bar", false},    // hyphenated but neither part resolves
		{"unknownword", false},
		{"", false},
		{"   ", false},
		{"8", false}, // a bare number with no unit lookahead classifies to zero tokens, not "recognized"
	}
	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			if got := IsRecognizedTerm(tt.word); got != tt.want {
				t.Errorf("IsRecognizedTerm(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}

// stringSlicesEqualUnordered compares two string slices ignoring order,
// for asserting two resolution paths produced the same SET of instance types.
func stringSlicesEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

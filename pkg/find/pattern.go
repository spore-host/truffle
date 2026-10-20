package find

import (
	"regexp"
	"strings"
)

// LooksLikePattern reports whether query is an instance-type pattern — an
// exact type ("r8g.2xlarge"), a bare family ("g5"), a glob ("m7i.*") or a
// regex ("c[6-8]i\..*") — rather than a natural-language query.
//
// [ParseQuery] applies it first, so a library caller gets the same
// pattern-then-parse dispatch as `truffle find` (#263). [PatternToRegex]
// turns such a query into the anchored regexp the search uses.
func LooksLikePattern(query string) bool {
	// A single word that fully resolves as known vocabulary — a GPU,
	// processor, or instruction-set term, including a hyphenated/suffixed
	// spec-string spelling like "RTX-PRO-6000", "A100-80GB", "H100!", "B200+"
	// (#130) — always takes the natural-language path, checked BEFORE the
	// wildcard/regex-indicator checks below. This has to come first: "+" is
	// one of the indicators looksLikeRegex treats as "this must be a regex",
	// so without this ordering "B200+"/"b200+" (a real, documented Modal
	// spec-string) matched looksLikeRegex and never reached the vocabulary
	// check that would have recognized it. No real GPU/processor/
	// instruction-set key contains '*' or '?' either, so checking vocabulary
	// first cannot misroute an actual glob/regex query.
	if !strings.Contains(query, " ") && IsRecognizedTerm(query) {
		return false
	}
	if strings.ContainsAny(query, "*?") {
		return true
	}
	if looksLikeRegex(query) {
		return true
	}
	// Single word that looks like an instance type (e.g. "m7i.large", "c6i",
	// "trn1.32xlarge"). The family prefix may be MULTIPLE letters before the
	// generation digit — AWS accelerator families are trn1/trn2 (Trainium),
	// inf1/inf2 (Inferentia), dl1 (Habana), vt1 (video). The old `^[a-z]\d`
	// (single leading letter) silently misrouted those to the natural-language
	// parser, which emitted a ".*" match-everything pattern and hung/returned the
	// whole catalog (#69-class bug). Match one-or-more leading letters + a
	// generation digit instead.
	if !strings.Contains(query, " ") {
		if matched, _ := regexp.MatchString(`(?i)^[a-z]+\d`, query); matched {
			return true
		}
		// A family whose name starts with a hyphenated word rather than a
		// generation digit ("u-3tb1", "mac-m4pro"), alone or with a size
		// ("u-6tb1.112xlarge", "mac-m4.metal"). The shape check above misses
		// these; the live exact-type gate found them (#263).
		lower := strings.ToLower(query)
		if i := strings.LastIndexByte(lower, '.'); i > 0 {
			lower = lower[:i]
		}
		if IsInstanceFamily(lower) {
			return true
		}
	}
	return false
}

// PatternToRegex converts an instance-type pattern (see [LooksLikePattern])
// to an anchored regexp source. If the pattern contains
// regex metacharacters ([, ], +, \d, \w, unanchored ^/$), it's treated as a
// regex directly. Otherwise it's treated as a glob where * and ? are wildcards.
func PatternToRegex(pattern string) string {
	// A bare family name means the whole family: "g5" -> every g5 size. It
	// used to become ^g5$, which no instance type matches (#229).
	if lower := strings.ToLower(pattern); IsInstanceFamily(lower) {
		return "^(" + regexp.QuoteMeta(lower) + `)\..*$`
	}
	if looksLikeRegex(pattern) {
		// Fix common mistake: bare * used as glob wildcard inside regex.
		// Replace "*" with ".*" when not preceded by a regex quantifier target
		// (e.g., "]*.xlarge" → "].*\\.xlarge")
		pattern = fixGlobStarsInRegex(pattern)
		// Unescaped dots in instance-type patterns are almost always literal
		// (e.g., "c7i.xlarge"), so escape dots that aren't part of ".*" or ".+"
		pattern = escapeLiteralDots(pattern)
		// Anchor if not already anchored
		if !strings.HasPrefix(pattern, "^") {
			pattern = "^" + pattern
		}
		if !strings.HasSuffix(pattern, "$") {
			pattern = pattern + "$"
		}
		return pattern
	}
	return WildcardToRegex(pattern)
}

// fixGlobStarsInRegex replaces bare "*" with ".*" in patterns that mix regex
// and glob syntax. A bare "*" is one not preceded by "." or another quantifier.
func fixGlobStarsInRegex(pattern string) string {
	var result strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '*' && (i == 0 || (pattern[i-1] != '.' && pattern[i-1] != '\\')) {
			result.WriteString(".*")
		} else {
			result.WriteByte(pattern[i])
		}
	}
	return result.String()
}

// escapeLiteralDots escapes dots that appear to be literal separators in
// instance type names (e.g., "c7i.xlarge") rather than regex wildcards.
// Dots that are part of ".*" or ".+" are left as-is.
func escapeLiteralDots(pattern string) string {
	var result strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '.' {
			if i+1 < len(pattern) && (pattern[i+1] == '*' || pattern[i+1] == '+') {
				// Part of .* or .+ — keep as regex wildcard
				result.WriteByte('.')
			} else if i > 0 && pattern[i-1] == '\\' {
				// Already escaped
				result.WriteByte('.')
			} else {
				// Likely a literal dot separator in instance type name
				result.WriteString("\\.")
			}
		} else {
			result.WriteByte(pattern[i])
		}
	}
	return result.String()
}

func looksLikeRegex(pattern string) bool {
	for _, indicator := range []string{"[", "]", "(", ")", "+", "\\d", "\\w", "\\s", "|"} {
		if strings.Contains(pattern, indicator) {
			return true
		}
	}
	return false
}

// WildcardToRegex converts a glob, where only * and ? are wildcards (and ".*"
// is read as "anything"), into an anchored regexp source.
func WildcardToRegex(pattern string) string {
	// Treat ".*" as a combined "match anything" before escaping, since users
	// commonly write "c7.*" meaning "everything starting with c7".
	pattern = strings.ReplaceAll(pattern, ".*", "\x00DOTSTAR\x00")
	// Escape special regex characters except * and ?
	pattern = regexp.QuoteMeta(pattern)
	// Replace wildcards
	pattern = strings.ReplaceAll(pattern, `\*`, ".*")
	pattern = strings.ReplaceAll(pattern, `\?`, ".")
	// Restore .* sequences
	pattern = strings.ReplaceAll(pattern, "\x00DOTSTAR\x00", ".*")
	// Anchor the pattern
	return "^" + pattern + "$"
}

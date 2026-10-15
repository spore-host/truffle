package find

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/metadata"
)

// UnrecognizedQueryError is returned by [ParseQuery] for a query that has
// words it doesn't recognise and nothing it does recognise that narrows the
// search (#229), e.g. "zzqx". Searching anyway would list, and price, every
// instance type in the region.
type UnrecognizedQueryError struct {
	Terms       []string          // the unrecognised words, in query order
	Suggestions map[string]string // term -> closest known word, where one is close
}

func (e *UnrecognizedQueryError) Error() string {
	parts := make([]string, len(e.Terms))
	for i, t := range e.Terms {
		parts[i] = fmt.Sprintf("%q", t)
		if s, ok := e.Suggestions[t]; ok {
			parts[i] += fmt.Sprintf(" (did you mean %q?)", s)
		}
	}
	noun := "term"
	if len(e.Terms) > 1 {
		noun = "terms"
	}
	return fmt.Sprintf("no recognised search terms; unknown %s: %s\n"+
		"Try a vendor (amd, intel, graviton), processor (turin, ice lake), GPU (h100, a10g), "+
		"family (g5, m7i), size (xlarge, large), or specs (16 cores, 64gb), "+
		"or an instance-type pattern such as \"g5.*\"",
		noun, strings.Join(parts, ", "))
}

func newUnrecognizedQueryError(terms []string) *UnrecognizedQueryError {
	return &UnrecognizedQueryError{Terms: terms, Suggestions: SuggestTerms(terms)}
}

// IsInstanceFamily reports whether word (lower case) names an instance family:
// one in the catalog snapshot, or one the Nitro table classifies.
func IsInstanceFamily(word string) bool {
	return metadata.IsInstanceFamily(word) || (!strings.Contains(word, ".") && aws.NitroGeneration(word) != aws.NitroGenerationUnknown)
}

// familyShaped matches words that look like an instance family (letters, a
// generation digit, optional suffix), for the pattern suggestion.
var familyShaped = regexp.MustCompile(`^[a-z]+[0-9][a-z0-9-]*$`)

// SuggestTerms returns, for each term with a close match in the query
// vocabulary, that match: the known word within edit distance 2 (1 for words
// of four letters or fewer) with the smallest distance, ties broken by the
// longest shared prefix, then alphabetically. A family-shaped term with no
// close match is suggested as a pattern ("zz9q" -> "zz9q.*"), since the
// family may be newer than truffle.
func SuggestTerms(terms []string) map[string]string {
	vocab := vocabulary()
	out := make(map[string]string)
	for _, term := range terms {
		t := strings.ToLower(term)
		limit := 2
		if len(t) <= 4 {
			limit = 1
		}
		best, bestD, bestP := "", limit+1, 0
		for _, w := range vocab {
			d := editDistance(t, w, limit)
			if p := sharedPrefix(t, w); d < bestD || (d == bestD && d <= limit && p > bestP) {
				best, bestD, bestP = w, d, p
			}
		}
		switch {
		case best != "" && best != t:
			out[term] = best
		case familyShaped.MatchString(t):
			out[term] = t + ".*"
		}
	}
	return out
}

// vocabulary is every single word the parser recognises on its own, sorted.
func vocabulary() []string {
	set := make(map[string]bool)
	add := func(w string) {
		if !strings.Contains(w, " ") {
			set[w] = true
		}
	}
	for k := range metadata.VendorAliases {
		add(k)
	}
	for k, info := range metadata.ProcessorDatabase {
		add(k)
		for _, f := range info.Families {
			add(f)
		}
	}
	for k, info := range metadata.GPUDatabase {
		add(k)
		for _, f := range info.Families {
			add(f)
		}
	}
	for k := range metadata.GPUAliases {
		add(k)
	}
	for k := range acceleratorWords {
		add(k)
	}
	for k := range metadata.InstructionSetDatabase {
		add(k)
	}
	for k := range metadata.InstructionSetAliases {
		add(k)
	}
	for k := range metadata.SizeCategories {
		add(k)
	}
	for k := range metadata.NetworkAliases {
		add(k)
	}
	for f := range aws.NitroFamilies() {
		add(f)
	}
	for k := range qualitativeKeywords {
		add(k)
	}
	for _, w := range []string{"efa", "mig", "x86_64", "arm64", "cores", "vcpus", "gpus", "xlarge", "nano", "micro", "metal"} {
		add(w)
	}
	words := make([]string, 0, len(set))
	for w := range set {
		words = append(words, w)
	}
	sort.Strings(words)
	return words
}

// editDistance is the Levenshtein distance between a and b, or limit+1 once
// it is known to exceed limit.
func editDistance(a, b string, limit int) int {
	if d := len(a) - len(b); d > limit || -d > limit {
		return limit + 1
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// sharedPrefix returns the length of the common prefix of a and b.
func sharedPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

package aws

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// maxFilterValuesPerCall is EC2's cap on the total number of filter values in
// one DescribeInstanceTypes request (exceeding it returns FilterLimitExceeded).
const maxFilterValuesPerCall = 200

// describeInputsFor turns a search's pattern and filter options into one or
// more DescribeInstanceTypes requests that narrow the catalog server-side
// (#195). Without this, a natural-language find pages through the whole
// catalog (about 11 pages) in every region and filters client-side.
//
// The server-side filters are only ever a superset of what the client-side
// checks accept: matcher and matchesFilters still run on every returned type
// and remain the source of truth. A constraint is only pushed down when EC2's
// filter semantics are known to agree with the client check:
//
//   - instance-type: the literal family prefixes (or exact names) the pattern
//     is anchored to, as EC2 wildcards such as "c8a.*". Patterns of any other
//     shape are not pushed down, and nor is any pattern when an "or efa"
//     alternative (FilterOptions.EFAAlternative) can admit types outside it.
//   - processor-info.supported-architecture: matches if any supported
//     architecture equals the value, as matchesFilters does.
//   - network-info.efa-supported: "true" when EFA is required; the same
//     NetworkInfo.EfaSupported flag matchesFilters checks (#253).
//   - vcpu-info.default-vcpus: only for an exact vCPU match. EC2 filters take
//     discrete values, not ranges, so minimum vCPU, memory, and core counts
//     stay client-side.
//
// The result is a single empty input (fetch everything) when nothing can be
// pushed down. Instance-type values are split across inputs so that no request
// exceeds maxFilterValuesPerCall; the caller unions the results.
func describeInputsFor(matcher *regexp.Regexp, opts FilterOptions) []*ec2.DescribeInstanceTypesInput {
	var common []types.Filter
	if opts.Architecture != "" {
		common = append(common, types.Filter{
			Name:   stringPtr("processor-info.supported-architecture"),
			Values: []string{opts.Architecture},
		})
	}
	if opts.RequireEFA {
		common = append(common, types.Filter{
			Name:   stringPtr("network-info.efa-supported"),
			Values: []string{"true"},
		})
	}
	if opts.ExactVCPUs && opts.MinVCPUs > 0 {
		common = append(common, types.Filter{
			Name:   stringPtr("vcpu-info.default-vcpus"),
			Values: []string{strconv.Itoa(opts.MinVCPUs)},
		})
	}

	var typeValues []string
	if matcher != nil && opts.EFAAlternative == nil {
		typeValues = instanceTypeFilterValues(matcher.String())
	}

	if len(typeValues) == 0 {
		return []*ec2.DescribeInstanceTypesInput{{Filters: common}}
	}

	chunk := maxFilterValuesPerCall - len(common)
	var inputs []*ec2.DescribeInstanceTypesInput
	for start := 0; start < len(typeValues); start += chunk {
		end := min(start+chunk, len(typeValues))
		filters := append([]types.Filter{{
			Name:   stringPtr("instance-type"),
			Values: typeValues[start:end],
		}}, common...)
		inputs = append(inputs, &ec2.DescribeInstanceTypesInput{Filters: filters})
	}
	return inputs
}

// instanceTypeFilterValues derives EC2 instance-type filter values from an
// anchored pattern, or returns nil if the pattern's shape isn't recognised.
// Every instance type the pattern can match is matched by at least one value.
// Recognised shapes, as built by find's BuildCriteria and the glob converter:
//
//	^(c8a|m8a)\..*$     -> c8a.*, m8a.*      (family alternation, any rest)
//	^(g5\.xlarge|p5\.48xlarge)$ -> g5.xlarge, p5.48xlarge (exact names)
//	^m7i.*$             -> m7i*              (single literal prefix)
//
// Anything with a top-level alternation outside the leading group, a
// quantifier applied to the prefix, or an EC2 wildcard character inside a
// literal is rejected, which simply means "no server-side type filter".
func instanceTypeFilterValues(pattern string) []string {
	if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") || strings.HasSuffix(pattern, `\$`) {
		return nil
	}
	body := pattern[1 : len(pattern)-1]

	var prefixes []string
	var rest string
	if strings.HasPrefix(body, "(") {
		end := strings.IndexByte(body, ')')
		if end < 0 {
			return nil
		}
		group := body[1:end]
		// An escaped ')' inside the group would make IndexByte stop early;
		// unquoteLiteral rejects the trailing lone backslash in that case.
		for _, alt := range strings.Split(group, "|") {
			lit, ok := unquoteLiteral(alt)
			if !ok || lit == "" {
				return nil
			}
			prefixes = append(prefixes, lit)
		}
		rest = body[end+1:]
	} else {
		i := literalPrefixLen(body)
		lit, ok := unquoteLiteral(body[:i])
		if !ok || lit == "" {
			return nil
		}
		prefixes = []string{lit}
		rest = body[i:]
	}

	if !restIsSafe(rest) {
		return nil
	}

	// Extend every prefix with any literal text that follows the group (the
	// "\." in "^(c8a|m8a)\..*$"), so "c8a.*" doesn't also fetch c8ad. Skip
	// this when a quantifier follows, since it would apply to the last char.
	if i := literalPrefixLen(rest); i > 0 && (i == len(rest) || !strings.ContainsRune("*+?{", rune(rest[i]))) {
		suffix, ok := unquoteLiteral(rest[:i])
		if !ok {
			return nil
		}
		for j := range prefixes {
			prefixes[j] += suffix
		}
		rest = rest[i:]
	}

	values := make([]string, 0, len(prefixes))
	seen := make(map[string]bool, len(prefixes))
	for _, p := range prefixes {
		// EC2 filter values treat * and ? as wildcards; a literal containing
		// either can't be expressed exactly.
		if strings.ContainsAny(p, "*?") {
			return nil
		}
		v := p
		if rest != "" {
			v = p + "*"
		}
		if !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	return values
}

// literalPrefixLen returns the length of the leading run of s made only of
// plain characters and escaped (backslash-quoted) punctuation.
func literalPrefixLen(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			if i+1 < len(s) && isQuotablePunct(s[i+1]) {
				i += 2
				continue
			}
			return i
		}
		if isRegexMeta(c) {
			return i
		}
		i++
	}
	return i
}

// unquoteLiteral reverses regexp.QuoteMeta for s, reporting false if s
// contains any regex syntax that isn't an escaped literal.
func unquoteLiteral(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) || !isQuotablePunct(s[i+1]) {
				return "", false
			}
			i++
			b.WriteByte(s[i])
			continue
		}
		if isRegexMeta(c) {
			return "", false
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// restIsSafe reports whether the part of the pattern after the literal prefix
// can only narrow the match: it must not start with a quantifier (which would
// make the prefix's last element optional or repeated) and must not contain a
// top-level alternation (which would let a branch skip the prefix).
func restIsSafe(rest string) bool {
	if rest == "" {
		return true
	}
	switch rest[0] {
	case '*', '+', '?', '{':
		return false
	}
	depth := 0
	inClass := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c == '\\':
			i++ // skip the escaped character
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth < 0 {
				return false
			}
		case c == '|' && depth == 0:
			return false
		}
	}
	return depth == 0 && !inClass
}

func isRegexMeta(c byte) bool {
	return strings.IndexByte(`\.+*?()|[]{}^$`, c) >= 0
}

// isQuotablePunct reports whether c is a character regexp.QuoteMeta escapes,
// i.e. whether `\c` denotes the literal c (as opposed to \d, \w, ...).
func isQuotablePunct(c byte) bool {
	return isRegexMeta(c) || c == '-'
}

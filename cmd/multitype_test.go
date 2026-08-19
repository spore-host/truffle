package cmd

import (
	"regexp"
	"testing"
)

// TestAllLookLikePatterns covers the decision between a multi-type
// side-by-side comparison (every arg individually looks like an instance-type
// pattern, #52) and a multi-word natural-language query whose words simply
// arrived as separate argv entries — those must NOT be routed to the
// multi-pattern comparison path, since "8" and "cores" individually aren't
// patterns but "8 cores" together is meaningful to find.ParseQuery.
func TestAllLookLikePatterns(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"three explicit types", []string{"g5.2xlarge", "g5.4xlarge", "g5.12xlarge"}, true},
		{"two explicit types", []string{"m7i.large", "c7i.large"}, true},
		{"type + glob", []string{"g5.2xlarge", "m7i*"}, true},
		{"NL query words", []string{"8", "cores"}, false},
		{"NL query with a pattern-shaped word mixed in", []string{"graviton", "8", "cores"}, false},
		{"single type (degenerate case)", []string{"g5.2xlarge"}, true},
		{"empty", nil, true}, // vacuously true; callers gate on len(args) > 1 separately
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allLookLikePatterns(tt.args); got != tt.want {
				t.Errorf("allLookLikePatterns(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

// TestCombinePatternsToRegex verifies the alternation actually matches every
// named pattern and nothing else, for both exact types and a mix of exact
// types and globs — the multi-type comparison (#52) is only correct if the
// combined regex doesn't accidentally widen or narrow what any one pattern
// alone would match.
func TestCombinePatternsToRegex(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		match    []string
		noMatch  []string
	}{
		{
			"three exact types",
			[]string{"g5.2xlarge", "g5.4xlarge", "g5.12xlarge"},
			[]string{"g5.2xlarge", "g5.4xlarge", "g5.12xlarge"},
			[]string{"g5.xlarge", "g5.8xlarge", "g6.2xlarge"},
		},
		{
			"exact type mixed with a glob",
			[]string{"g5.2xlarge", "m7i*"},
			[]string{"g5.2xlarge", "m7i.large", "m7i.24xlarge"},
			[]string{"g5.4xlarge", "c7i.large"},
		},
		{
			"single pattern (degenerate case)",
			[]string{"c6i.large"},
			[]string{"c6i.large"},
			[]string{"c6i.xlarge", "c7i.large"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			combined := combinePatternsToRegex(tt.patterns)
			re, err := regexp.Compile(combined)
			if err != nil {
				t.Fatalf("combinePatternsToRegex(%v) = %q, invalid regex: %v", tt.patterns, combined, err)
			}
			for _, m := range tt.match {
				if !re.MatchString(m) {
					t.Errorf("combined pattern %q should match %q (regex %q)", tt.patterns, m, combined)
				}
			}
			for _, nm := range tt.noMatch {
				if re.MatchString(nm) {
					t.Errorf("combined pattern %q should NOT match %q (regex %q)", tt.patterns, nm, combined)
				}
			}
		})
	}
}

// TestSpotCmd_AcceptsMultipleArgs is the regression test for #52's spot half:
// `spot` used to reject anything beyond a single pattern outright
// (cobra.ExactArgs(1)) — this pins the Args validator now accepts 1+ without
// needing a live AWS call.
func TestSpotCmd_AcceptsMultipleArgs(t *testing.T) {
	if err := spotCmd.Args(spotCmd, []string{"g5.2xlarge", "g5.4xlarge", "g5.12xlarge"}); err != nil {
		t.Errorf("spotCmd.Args rejected 3 explicit instance types: %v", err)
	}
	if err := spotCmd.Args(spotCmd, []string{"g5.2xlarge"}); err != nil {
		t.Errorf("spotCmd.Args rejected the pre-existing single-pattern case: %v", err)
	}
	if err := spotCmd.Args(spotCmd, nil); err == nil {
		t.Error("spotCmd.Args accepted zero args, want an error (at least one pattern required)")
	}
}

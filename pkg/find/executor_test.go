package find

import (
	"strings"
	"testing"
)

// extractFamilySet pulls the pipe-separated alternatives out of the first
// "(...)" group in a compiled pattern string, e.g. "^(m6i|c6i)\\..*$" ->
// {"m6i": true, "c6i": true}. Used to compare patterns by family-set content
// rather than by exact string (family order within the group isn't
// significant, since it comes from map iteration).
func extractFamilySet(pattern string) map[string]bool {
	start := strings.Index(pattern, "(")
	end := strings.Index(pattern, ")")
	set := make(map[string]bool)
	if start == -1 || end == -1 || end <= start {
		return set
	}
	for _, part := range strings.Split(pattern[start+1:end], "|") {
		set[part] = true
	}
	return set
}

func TestBuildCriteria(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		wantPattern   string
		wantArch      string
		wantMinVCPU   int
		wantMinMemory float64
	}{
		{
			name:        "graviton query",
			query:       "graviton",
			wantPattern: "^(a1|m6g|c6g|r6g|t4g|m6gd|c6gd|r6gd|c6gn|im4gn|is4gen|x2gd|m7g|c7g|r7g|c7gn|hpc7g|c7gd|m7gd|r7gd|r8g)\\..*$",
			wantArch:    "arm64",
		},
		{
			name:        "ice lake query",
			query:       "ice lake",
			wantPattern: "^(m6i|c6i|r6i|r6id|r6idn|m6id|m6idn|c6id|c6in)\\..*$",
			wantArch:    "x86_64",
		},
		{
			name:        "a100 gpu",
			query:       "a100",
			wantPattern: "^(p4d\\.24xlarge|p4de\\.24xlarge)$",
		},
		{
			name:        "amd 16 cores",
			query:       "amd 16 cores",
			wantMinVCPU: 16,
			wantArch:    "x86_64",
		},
		{
			name:          "graviton 32gb",
			query:         "graviton 32gb",
			wantArch:      "arm64",
			wantMinMemory: 32,
		},
		{
			name:        "graviton large",
			query:       "graviton large",
			wantPattern: "^(a1|m6g|c6g|r6g|t4g|m6gd|c6gd|r6gd|c6gn|im4gn|is4gen|x2gd|m7g|c7g|r7g|c7gn|hpc7g|c7gd|m7gd|r7gd|r8g)\\.(2xlarge|4xlarge)$",
			wantArch:    "arm64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}

			criteria, err := pq.BuildCriteria(true)
			if err != nil {
				t.Fatalf("BuildCriteria() error = %v", err)
			}

			if tt.wantPattern != "" {
				if criteria.InstanceTypePattern == nil {
					t.Fatal("InstanceTypePattern is nil")
				}
				// Subset check, not exact-set equality: ProcessorDatabase/
				// GPUDatabase family lists grow over time (new AZ/storage
				// variants, new generations) without that being a behavior
				// regression, so pin "these families must be present" rather
				// than "exactly these families and no others".
				got := criteria.InstanceTypePattern.String()
				gotFamilies := extractFamilySet(got)
				for f := range extractFamilySet(tt.wantPattern) {
					if !gotFamilies[f] {
						t.Errorf("pattern = %q missing family %q; want pattern including %q", got, f, tt.wantPattern)
					}
				}
			}

			if criteria.FilterOptions.Architecture != tt.wantArch {
				t.Errorf("Architecture = %v, want %v",
					criteria.FilterOptions.Architecture, tt.wantArch)
			}

			if criteria.FilterOptions.MinVCPUs != tt.wantMinVCPU {
				t.Errorf("MinVCPUs = %v, want %v",
					criteria.FilterOptions.MinVCPUs, tt.wantMinVCPU)
			}

			if criteria.FilterOptions.MinMemory != tt.wantMinMemory {
				t.Errorf("MinMemory = %v, want %v",
					criteria.FilterOptions.MinMemory, tt.wantMinMemory)
			}
		})
	}
}

// TestBuildCriteria_IncludeAZs is the regression guard for truffle#141:
// BuildCriteria's includeAZs parameter must reach FilterOptions.IncludeAZs
// directly — this is what pkg/aws.SearchInstanceTypes uses to decide whether
// to do the expensive per-matched-type AZ lookup. Before the fix, this was
// hardcoded true regardless of what the caller (cmd/find.go's --skip-azs)
// asked for.
func TestBuildCriteria_IncludeAZs(t *testing.T) {
	pq, err := ParseQuery("graviton")
	if err != nil {
		t.Fatalf("ParseQuery() error = %v", err)
	}

	withAZs, err := pq.BuildCriteria(true)
	if err != nil {
		t.Fatalf("BuildCriteria(true) error = %v", err)
	}
	if !withAZs.FilterOptions.IncludeAZs {
		t.Error("BuildCriteria(true).FilterOptions.IncludeAZs = false, want true")
	}

	withoutAZs, err := pq.BuildCriteria(false)
	if err != nil {
		t.Fatalf("BuildCriteria(false) error = %v", err)
	}
	if withoutAZs.FilterOptions.IncludeAZs {
		t.Error("BuildCriteria(false).FilterOptions.IncludeAZs = true, want false (--skip-azs must reach search time, not just table display)")
	}
}

// TestBuildInstanceTypePattern_GPUWithOtherDimensions regression-guards the
// GPU early-return bug found while planning #144: buildInstanceTypePattern
// used to jump straight to GPU's exact-instance-type list whenever any GPU
// term was present, discarding every other active dimension (EFA, MIG,
// vendor, processor, instruction set, network speed) unconditionally.
func TestBuildInstanceTypePattern_GPUWithOtherDimensions(t *testing.T) {
	pq, err := ParseQuery("h100 efa")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	criteria, err := pq.BuildCriteria(true)
	if err != nil {
		t.Fatalf("BuildCriteria error: %v", err)
	}
	matcher := criteria.Matcher()
	if !matcher("p5.48xlarge") {
		t.Error("matcher(\"p5.48xlarge\") = false, want true (H100 family, EFA-capable)")
	}
	if matcher("c6a.large") {
		t.Error("matcher(\"c6a.large\") = true, want false (EFA-capable but not H100's family)")
	}
	if matcher("p4d.24xlarge") {
		t.Error("matcher(\"p4d.24xlarge\") = true, want false (A100, not H100)")
	}
}

func TestSearchCriteria_Matcher(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		instanceType string
		wantMatch    bool
	}{
		{
			name:         "graviton matches m6g",
			query:        "graviton",
			instanceType: "m6g.2xlarge",
			wantMatch:    true,
		},
		{
			name:         "graviton does not match m6i",
			query:        "graviton",
			instanceType: "m6i.2xlarge",
			wantMatch:    false,
		},
		{
			name:         "ice lake matches m6i",
			query:        "ice lake",
			instanceType: "m6i.4xlarge",
			wantMatch:    true,
		},
		{
			name:         "a100 matches exact instance",
			query:        "a100",
			instanceType: "p4d.24xlarge",
			wantMatch:    true,
		},
		{
			name:         "a100 does not match p3",
			query:        "a100",
			instanceType: "p3.2xlarge",
			wantMatch:    false,
		},
		{
			name:         "large matches 2xlarge",
			query:        "graviton large",
			instanceType: "m6g.2xlarge",
			wantMatch:    true,
		},
		{
			name:         "large does not match xlarge",
			query:        "graviton large",
			instanceType: "m6g.xlarge",
			wantMatch:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}

			criteria, err := pq.BuildCriteria(true)
			if err != nil {
				t.Fatalf("BuildCriteria() error = %v", err)
			}

			matcher := criteria.Matcher()
			got := matcher(tt.instanceType)

			if got != tt.wantMatch {
				t.Errorf("Matcher(%q) = %v, want %v", tt.instanceType, got, tt.wantMatch)
			}
		})
	}
}

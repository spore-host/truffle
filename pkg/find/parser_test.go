package find

import (
	"strings"
	"testing"
)

func TestParseQuery(t *testing.T) {
	tests := []struct {
		name            string
		query           string
		wantVendors     []string
		wantProcs       []string
		wantGPUs        []string
		wantSizes       []string
		wantVCPU        int
		wantMemory      float64
		wantArch        string
		wantNetworkGbps int
		wantEFA         bool
		wantErr         bool
	}{
		{
			name:        "single vendor",
			query:       "intel",
			wantVendors: []string{"intel"},
		},
		{
			name:        "single vendor - graviton",
			query:       "graviton",
			wantVendors: []string{"aws"},
		},
		{
			name:      "processor code name",
			query:     "ice lake",
			wantProcs: []string{"ice lake"},
		},
		{
			name:      "processor code name - milan",
			query:     "milan",
			wantProcs: []string{"milan"},
		},
		{
			name:      "multi-word processor",
			query:     "sapphire rapids",
			wantProcs: []string{"sapphire rapids"},
		},
		{
			name:     "gpu type",
			query:    "a100",
			wantGPUs: []string{"a100"},
		},
		{
			name:     "gpu alias",
			query:    "inf",
			wantGPUs: []string{"inferentia"},
		},
		{
			name:      "size category",
			query:     "large",
			wantSizes: []string{"large"},
		},
		{
			name:        "vendor with vcpu",
			query:       "amd 16 cores",
			wantVendors: []string{"amd"},
			wantVCPU:    16,
		},
		{
			name:        "vendor with memory",
			query:       "graviton 32gb",
			wantVendors: []string{"aws"},
			wantMemory:  32,
		},
		{
			name:        "combined specs",
			query:       "amd 16 cores 64gb",
			wantVendors: []string{"amd"},
			wantVCPU:    16,
			wantMemory:  64,
		},
		{
			name:        "vendor and size",
			query:       "graviton large",
			wantVendors: []string{"aws"},
			wantSizes:   []string{"large"},
		},
		{
			name:      "processor with specs",
			query:     "milan 64 cores",
			wantProcs: []string{"milan"},
			wantVCPU:  64,
		},
		{
			name:     "architecture",
			query:    "arm64",
			wantArch: "arm64",
		},
		{
			name:     "x86_64 architecture",
			query:    "x86_64",
			wantArch: "x86_64",
		},
		{
			name:     "multi-word gpu",
			query:    "radeon pro v520",
			wantGPUs: []string{"radeon pro v520"},
		},
		{
			name:    "empty query",
			query:   "",
			wantErr: true,
		},
		{
			name:     "vcpu with different unit",
			query:    "8 vcpus",
			wantVCPU: 8,
		},
		{
			name:       "memory with gib",
			query:      "32gib",
			wantMemory: 32,
		},
		{
			name:    "efa network",
			query:   "efa",
			wantEFA: true,
		},
		{
			name:            "100gbps network",
			query:           "100gbps",
			wantNetworkGbps: 100,
		},
		{
			name:        "efa with graviton",
			query:       "efa graviton",
			wantVendors: []string{"aws"},
			wantEFA:     true,
		},
		{
			name:     "h100 with efa",
			query:    "h100 efa",
			wantGPUs: []string{"h100"},
			wantEFA:  true,
		},
		{
			name:            "100g alias",
			query:           "100g",
			wantNetworkGbps: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseQuery(tt.query)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseQuery() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if !stringSlicesEqual(got.Vendors, tt.wantVendors) {
				t.Errorf("Vendors = %v, want %v", got.Vendors, tt.wantVendors)
			}

			if !stringSlicesEqual(got.Processors, tt.wantProcs) {
				t.Errorf("Processors = %v, want %v", got.Processors, tt.wantProcs)
			}

			if !stringSlicesEqual(got.GPUs, tt.wantGPUs) {
				t.Errorf("GPUs = %v, want %v", got.GPUs, tt.wantGPUs)
			}

			if !stringSlicesEqual(got.Sizes, tt.wantSizes) {
				t.Errorf("Sizes = %v, want %v", got.Sizes, tt.wantSizes)
			}

			if got.MinVCPU != tt.wantVCPU {
				t.Errorf("MinVCPU = %v, want %v", got.MinVCPU, tt.wantVCPU)
			}

			if got.MinMemory != tt.wantMemory {
				t.Errorf("MinMemory = %v, want %v", got.MinMemory, tt.wantMemory)
			}

			if got.Architecture != tt.wantArch {
				t.Errorf("Architecture = %v, want %v", got.Architecture, tt.wantArch)
			}

			if got.MinNetworkGbps != tt.wantNetworkGbps {
				t.Errorf("MinNetworkGbps = %v, want %v", got.MinNetworkGbps, tt.wantNetworkGbps)
			}

			if got.RequireEFA != tt.wantEFA {
				t.Errorf("RequireEFA = %v, want %v", got.RequireEFA, tt.wantEFA)
			}
		})
	}
}

func TestParsedQuery_ResolveInstanceFamilies(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantMin      int
		wantFamilies []string
	}{
		{
			name:    "graviton",
			query:   "graviton",
			wantMin: 10,
		},
		{
			name:         "ice lake",
			query:        "ice lake",
			wantMin:      3,
			wantFamilies: []string{"m6i", "c6i", "r6i"},
		},
		{
			name:         "a100",
			query:        "a100",
			wantMin:      1,
			wantFamilies: []string{"p4d", "p4de"},
		},
		{
			name:    "intel vendor",
			query:   "intel",
			wantMin: 10,
		},
		{
			name:         "avx-512",
			query:        "avx-512",
			wantMin:      5,
			wantFamilies: []string{"m6i", "m7i", "m7a"},
		},
		{
			name:         "sve2",
			query:        "sve2",
			wantMin:      1,
			wantFamilies: []string{"r8g"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}

			families := pq.ResolveInstanceFamilies()

			if len(families) < tt.wantMin {
				t.Errorf("ResolveInstanceFamilies() returned %d families, want >= %d",
					len(families), tt.wantMin)
			}

			if len(tt.wantFamilies) > 0 {
				for _, wantFamily := range tt.wantFamilies {
					found := false
					for _, family := range families {
						if family == wantFamily {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("ResolveInstanceFamilies() missing family %q in %v",
							wantFamily, families)
					}
				}
			}
		})
	}
}

func TestParsedQuery_DeriveArchitecture(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantArch string
	}{
		{
			name:     "graviton",
			query:    "graviton",
			wantArch: "arm64",
		},
		{
			name:     "intel",
			query:    "intel",
			wantArch: "x86_64",
		},
		{
			name:     "ice lake",
			query:    "ice lake",
			wantArch: "x86_64",
		},
		{
			name:     "milan",
			query:    "milan",
			wantArch: "x86_64",
		},
		{
			name:     "explicit arm64",
			query:    "arm64",
			wantArch: "arm64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}

			arch := pq.DeriveArchitecture()
			if arch != tt.wantArch {
				t.Errorf("DeriveArchitecture() = %v, want %v", arch, tt.wantArch)
			}
		})
	}
}

func TestParseQuery_InstructionSetToken(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    []string
		wantErr bool
	}{
		{name: "avx2", query: "avx2", want: []string{"avx2"}},
		{name: "avx-512 canonical", query: "avx-512", want: []string{"avx-512"}},
		{name: "avx512 alias (no hyphen)", query: "avx512", want: []string{"avx-512"}},
		{name: "avx 512 two-word alias", query: "avx 512", want: []string{"avx-512"}},
		{name: "sve", query: "sve", want: []string{"sve"}},
		{name: "sve2", query: "sve2", want: []string{"sve2"}},
		{name: "instruction set combined with spec", query: "avx-512 8 cores", want: []string{"avx-512"}, wantErr: false},
		{
			name:    "sve2 conflicts with explicit x86_64",
			query:   "sve2 x86_64",
			wantErr: true,
		},
		{
			name:    "avx-512 conflicts with graviton",
			query:   "avx-512 graviton",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseQuery(%q) error = nil, want error (conflicting architecture)", tt.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseQuery(%q) error = %v", tt.query, err)
			}
			if len(pq.InstructionSets) != len(tt.want) {
				t.Fatalf("InstructionSets = %v, want %v", pq.InstructionSets, tt.want)
			}
			for i, w := range tt.want {
				if pq.InstructionSets[i] != w {
					t.Errorf("InstructionSets[%d] = %q, want %q", i, pq.InstructionSets[i], w)
				}
			}
		})
	}
}

// TestParseQuery_MIGToken confirms "mig" parses as a boolean capability
// filter (RequireMIG), the same shape as the existing "efa" token — not a
// named term with a Value like GPUs/instruction sets (#143).
func TestParseQuery_MIGToken(t *testing.T) {
	pq, err := ParseQuery("mig")
	if err != nil {
		t.Fatalf("ParseQuery(\"mig\") error = %v", err)
	}
	if !pq.RequireMIG {
		t.Error("RequireMIG = false, want true")
	}

	pq2, err := ParseQuery("h100")
	if err != nil {
		t.Fatalf("ParseQuery(\"h100\") error = %v", err)
	}
	if pq2.RequireMIG {
		t.Error("RequireMIG = true for a query that never mentioned mig, want false")
	}
}

// TestParseQuery_MPSAlias confirms "mps" resolves as a GPU term (aliasing to
// "nvidia") rather than as a MIG-style capability flag — MPS runs on any
// NVIDIA GPU, so it's a synonym, not a filter (#143).
func TestParseQuery_MPSAlias(t *testing.T) {
	pq, err := ParseQuery("mps")
	if err != nil {
		t.Fatalf("ParseQuery(\"mps\") error = %v", err)
	}
	if len(pq.GPUs) != 1 || pq.GPUs[0] != "nvidia" {
		t.Errorf("GPUs = %v, want [\"nvidia\"]", pq.GPUs)
	}
	if pq.RequireMIG {
		t.Error("RequireMIG = true for \"mps\", want false (MPS is not a MIG-style capability filter)")
	}
}

// TestResolveInstanceFamilies_MIG confirms "mig" resolves to real MIG-capable
// families and excludes a known non-MIG-capable one (#143).
func TestResolveInstanceFamilies_MIG(t *testing.T) {
	pq, err := ParseQuery("mig")
	if err != nil {
		t.Fatalf("ParseQuery(\"mig\") error = %v", err)
	}
	families := pq.ResolveInstanceFamilies()
	familySet := make(map[string]bool, len(families))
	for _, f := range families {
		familySet[f] = true
	}
	if !familySet["p5"] {
		t.Errorf("ResolveInstanceFamilies() for \"mig\" missing p5 (H100, MIG-capable): %v", families)
	}
	if familySet["g6"] {
		t.Errorf("ResolveInstanceFamilies() for \"mig\" should not include g6 (L4, not MIG-capable): %v", families)
	}
}

func TestParsedQuery_DeriveArchitecture_InstructionSet(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantArch string
	}{
		{name: "avx2 implies x86_64", query: "avx2", wantArch: "x86_64"},
		{name: "avx-512 implies x86_64", query: "avx-512", wantArch: "x86_64"},
		{name: "sve implies arm64", query: "sve", wantArch: "arm64"},
		{name: "sve2 implies arm64", query: "sve2", wantArch: "arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}
			if arch := pq.DeriveArchitecture(); arch != tt.wantArch {
				t.Errorf("DeriveArchitecture() = %v, want %v", arch, tt.wantArch)
			}
		})
	}
}

func TestParsedQuery_BuildSizePattern(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantContains []string
	}{
		{
			name:         "large",
			query:        "large",
			wantContains: []string{"2xlarge", "4xlarge"},
		},
		{
			name:         "small",
			query:        "small",
			wantContains: []string{"small", "medium"},
		},
		{
			name:         "no size",
			query:        "intel",
			wantContains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}

			pattern := pq.BuildSizePattern()

			if len(tt.wantContains) == 0 {
				if pattern != ".*" {
					t.Errorf("BuildSizePattern() = %v, want .*", pattern)
				}
				return
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(pattern, want) {
					t.Errorf("BuildSizePattern() = %v, should contain %v", pattern, want)
				}
			}
		})
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseQuery_AppToken(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantApps []string
	}{
		{"app by canonical name", "paraview", []string{"paraview"}},
		{"app by alias", "pv", []string{"paraview"}},
		{"app by alias imagej", "imagej", []string{"fiji"}},
		{"app case insensitive", "ParaView", []string{"paraview"}},
		{"non-app word is not app", "nvidia", nil},
		{"unknown word is not app", "notarealapplication", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q) error = %v", tt.query, err)
			}
			if len(pq.Apps) != len(tt.wantApps) {
				t.Errorf("Apps = %v, want %v", pq.Apps, tt.wantApps)
				return
			}
			for i, want := range tt.wantApps {
				if pq.Apps[i] != want {
					t.Errorf("Apps[%d] = %q, want %q", i, pq.Apps[i], want)
				}
			}
		})
	}
}

func TestResolveInstanceFamilies_AppToken(t *testing.T) {
	pq, err := ParseQuery("paraview")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	families := pq.ResolveInstanceFamilies()
	if len(families) == 0 {
		t.Error("ResolveInstanceFamilies() returned empty for paraview")
	}
	// ParaView catalog entry specifies g6, g5, g4dn
	found := false
	for _, f := range families {
		if f == "g6" || f == "g5" || f == "g4dn" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected g6/g5/g4dn families for paraview, got %v", families)
	}
}

func TestBuildCriteria_AppMinHardware(t *testing.T) {
	// ParaView: min_vcpus=4, min_memory_gib=16
	pq, err := ParseQuery("paraview")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	criteria, err := pq.BuildCriteria(true)
	if err != nil {
		t.Fatalf("BuildCriteria error: %v", err)
	}
	if criteria.FilterOptions.MinVCPUs < 4 {
		t.Errorf("MinVCPUs = %d, want >= 4", criteria.FilterOptions.MinVCPUs)
	}
	if criteria.FilterOptions.MinMemory < 16 {
		t.Errorf("MinMemory = %.0f, want >= 16", criteria.FilterOptions.MinMemory)
	}
}

func TestBuildCriteria_AppDoesNotOverrideExplicit(t *testing.T) {
	// Explicit 32 vCPUs should override the app's 4 minimum
	pq, err := ParseQuery("paraview 32 vcpus")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	if pq.MinVCPU != 32 {
		t.Fatalf("Expected MinVCPU=32, got %d", pq.MinVCPU)
	}
	criteria, err := pq.BuildCriteria(true)
	if err != nil {
		t.Fatalf("BuildCriteria error: %v", err)
	}
	if criteria.FilterOptions.MinVCPUs != 32 {
		t.Errorf("MinVCPUs = %d, want 32 (explicit override)", criteria.FilterOptions.MinVCPUs)
	}
}

func TestParseQuery_NestedVirtToken(t *testing.T) {
	for _, q := range []string{"nested-virt", "nested-virtualization", "nestedvirt"} {
		pq, err := ParseQuery(q)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", q, err)
		}
		if !pq.RequireNestedV {
			t.Errorf("ParseQuery(%q): RequireNestedV = false, want true", q)
		}
	}
	// A query without the keyword must not set it.
	pq, err := ParseQuery("intel 8 vcpu")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if pq.RequireNestedV {
		t.Error("RequireNestedV should be false when not requested")
	}
}

// TestBuildCriteria_NestedVirt confirms the parsed flag flows into FilterOptions.
func TestBuildCriteria_NestedVirt(t *testing.T) {
	pq, err := ParseQuery("nested-virt 16 vcpu")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	sc, err := pq.BuildCriteria(true)
	if err != nil {
		t.Fatalf("BuildCriteria: %v", err)
	}
	if !sc.FilterOptions.NestedVirt {
		t.Error("FilterOptions.NestedVirt should be true")
	}
}

// TestParseQuery_AndOrTokens confirms "and"/"or" classify as TokenAnd/TokenOr
// (not TokenUnknown) and set pq.Operator accordingly (#144).
func TestParseQuery_AndOrTokens(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantOperator DimensionOperator
	}{
		{"no operator defaults to AND", "h100 efa", OperatorAnd},
		{"explicit or", "h100 or efa", OperatorOr},
		{"explicit and is a no-op vs default", "h100 and efa", OperatorAnd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pq, err := ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q) error = %v", tt.query, err)
			}
			if pq.Operator != tt.wantOperator {
				t.Errorf("Operator = %v, want %v", pq.Operator, tt.wantOperator)
			}
			for _, tok := range pq.RawTokens {
				if tok.Raw == "and" && tok.Type != TokenAnd {
					t.Errorf("token %q classified as %v, want TokenAnd", tok.Raw, tok.Type)
				}
				if tok.Raw == "or" && tok.Type != TokenOr {
					t.Errorf("token %q classified as %v, want TokenOr", tok.Raw, tok.Type)
				}
			}
		})
	}
}

// TestResolveInstanceFamilies_ANDByDefault confirms cross-dimension
// combination defaults to intersection (#144): "h100 efa" must resolve to
// exactly H100's own family (p5, which is also EFA-capable), not the full
// EFA-capable family list.
func TestResolveInstanceFamilies_ANDByDefault(t *testing.T) {
	pq, err := ParseQuery("h100 efa")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	families := pq.ResolveInstanceFamilies()
	if len(families) != 1 || families[0] != "p5" {
		t.Errorf("ResolveInstanceFamilies() for \"h100 efa\" = %v, want exactly [p5]", families)
	}

	// A combo whose dimensions share no family (Graviton vendor vs. NVIDIA
	// MIG-capable GPU families) must resolve to zero results under AND, not
	// silently fall back to union.
	pq2, err := ParseQuery("graviton mig")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	if families2 := pq2.ResolveInstanceFamilies(); len(families2) != 0 {
		t.Errorf("ResolveInstanceFamilies() for \"graviton mig\" = %v, want empty (Graviton and MIG-capable families never overlap)", families2)
	}
}

// TestResolveInstanceFamilies_ExplicitOr confirms "or" restores the old
// union behavior across dimensions (#144).
func TestResolveInstanceFamilies_ExplicitOr(t *testing.T) {
	pqAnd, err := ParseQuery("h100 efa")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	pqOr, err := ParseQuery("h100 or efa")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	andFamilies := pqAnd.ResolveInstanceFamilies()
	orFamilies := pqOr.ResolveInstanceFamilies()
	if len(orFamilies) <= len(andFamilies) {
		t.Errorf("ResolveInstanceFamilies() for \"h100 or efa\" (%d families) should be strictly larger than \"h100 efa\" (%d families)", len(orFamilies), len(andFamilies))
	}
	orSet := make(map[string]bool, len(orFamilies))
	for _, f := range orFamilies {
		orSet[f] = true
	}
	if !orSet["p5"] {
		t.Errorf("ResolveInstanceFamilies() for \"h100 or efa\" missing p5 (H100's family): %v", orFamilies)
	}
	if !orSet["c6a"] {
		t.Errorf("ResolveInstanceFamilies() for \"h100 or efa\" missing c6a (an EFA-capable family unrelated to H100): %v", orFamilies)
	}
}

// TestResolveInstanceFamilies_WithinDimensionStaysOR confirms multiple values
// within a single dimension (e.g. two GPU terms) still union together
// regardless of the cross-dimension operator default (#144) — no instance
// can be both A100 and H100, so this must never become an AND.
func TestResolveInstanceFamilies_WithinDimensionStaysOR(t *testing.T) {
	pq, err := ParseQuery("a100 h100")
	if err != nil {
		t.Fatalf("ParseQuery error: %v", err)
	}
	families := pq.ResolveInstanceFamilies()
	set := make(map[string]bool, len(families))
	for _, f := range families {
		set[f] = true
	}
	if !set["p4d"] || !set["p4de"] || !set["p5"] {
		t.Errorf("ResolveInstanceFamilies() for \"a100 h100\" = %v, want union including p4d, p4de, p5", families)
	}
}

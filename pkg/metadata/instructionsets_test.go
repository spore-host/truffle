package metadata

import "testing"

func TestInstructionSetDatabase(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		wantArch     string
		wantFamilies int // minimum expected families
		mustInclude  []string
		mustExclude  []string
	}{
		{
			name:         "avx2",
			key:          "avx2",
			wantArch:     "x86_64",
			wantFamilies: 10,
			mustInclude:  []string{"m6i", "m7i", "m6a", "m7a"},
		},
		{
			name:         "avx-512",
			key:          "avx-512",
			wantArch:     "x86_64",
			wantFamilies: 5,
			mustInclude:  []string{"m6i", "m7i", "m7a", "m8a"},
			// Milan/Rome/Zen3 (m6a/c6a/r6a, m5a/c5a/r5a) predate AMD's AVX-512
			// support (introduced with Genoa/Zen4); Skylake/Cascade Lake (m5,
			// c5, r5) predate the extensions ("VNNI"/"GFNI"/"VAES") this
			// database's AVX-512 entry is scoped to.
			mustExclude: []string{"m6a", "c6a", "r6a", "m5a", "c5a", "r5a", "m5", "c5", "r5"},
		},
		{
			name:         "sve",
			key:          "sve",
			wantArch:     "arm64",
			wantFamilies: 3,
			mustInclude:  []string{"m7g", "c7g", "r7g"},
			// Graviton/Graviton2 predate SVE (NEON only).
			mustExclude: []string{"a1", "m6g", "c6g", "r6g", "t4g"},
		},
		{
			name:         "sve2",
			key:          "sve2",
			wantArch:     "arm64",
			wantFamilies: 1,
			mustInclude:  []string{"r8g"},
			// Graviton3/3E have SVE but not SVE2.
			mustExclude: []string{"m7g", "c7g", "r7g"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, ok := InstructionSetDatabase[tt.key]
			if !ok {
				t.Fatalf("instruction set %q not found in database", tt.key)
			}
			if info.Architecture != tt.wantArch {
				t.Errorf("architecture = %v, want %v", info.Architecture, tt.wantArch)
			}
			if len(info.Families) < tt.wantFamilies {
				t.Errorf("families count = %v, want >= %v", len(info.Families), tt.wantFamilies)
			}
			familySet := make(map[string]bool, len(info.Families))
			for _, f := range info.Families {
				familySet[f] = true
			}
			for _, f := range tt.mustInclude {
				if !familySet[f] {
					t.Errorf("expected family %q in %q's family list", f, tt.key)
				}
			}
			for _, f := range tt.mustExclude {
				if familySet[f] {
					t.Errorf("family %q should NOT be in %q's family list", f, tt.key)
				}
			}
		})
	}
}

func TestInstructionSetAliases(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"avx512", "avx-512"},
		{"avx-512f", "avx-512"},
		{"avx 512", "avx-512"},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, ok := InstructionSetAliases[tt.alias]
			if !ok {
				t.Fatalf("alias %q not found", tt.alias)
			}
			if got != tt.want {
				t.Errorf("InstructionSetAliases[%q] = %v, want %v", tt.alias, got, tt.want)
			}
			if _, ok := InstructionSetDatabase[got]; !ok {
				t.Errorf("alias %q resolves to %q, which is not a real InstructionSetDatabase key", tt.alias, got)
			}
		})
	}
}

func TestGetFamiliesByInstructionSet(t *testing.T) {
	if got := GetFamiliesByInstructionSet("sve2"); len(got) == 0 {
		t.Error("GetFamiliesByInstructionSet(\"sve2\") returned no families")
	}
	if got := GetFamiliesByInstructionSet("does-not-exist"); got != nil {
		t.Errorf("GetFamiliesByInstructionSet(\"does-not-exist\") = %v, want nil", got)
	}
}

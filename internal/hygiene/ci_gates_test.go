// Package hygiene holds tests that assert on repo wiring rather than on code.
//
// It exists because wiring is what rots: a CI step is a one-line deletion whose
// absence is completely silent — nothing fails, the tree simply starts drifting
// again, and nobody notices until an unrelated PR picks up a reformatting diff.
// That is exactly how 7 files came to sit unformatted on main (#122).
// There are no non-test files here by design; the assertions are about the repo.
package hygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCIGatesFormatting checks that the formatting gate is still wired into CI,
// and that it REPORTS drift rather than fixing it.
//
// The second half is the subtle one. `gofmt -w` rewrites files and exits 0, so a
// "gate" built on it can only ever report success — green on a dirty tree
// forever, indistinguishable from having no gate at all. Only `gofmt -l`/`-d`
// can fail. `make check-fmt` is what this test actually verifies is called.
func TestCIGatesFormatting(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	step, ok := workflowStep(string(data), "Format gate")
	if !ok {
		t.Fatal("the CI workflow has no 'Format gate' step; without it nothing " +
			"prevents unformatted code from reaching main (#122)")
	}
	if !strings.Contains(step, "make check-fmt") {
		t.Error("the Format gate does not run 'make check-fmt'")
	}
}

// TestActionsArePinnedToSHAs: every `uses:` in a workflow must name a full
// 40-hex commit SHA with an EXACT `# vX.Y.Z` comment, not a tag and not a bare
// major.
//
// A tag is mutable — that half is why we pin at all. But a pin comment being
// PRESENT is not the same as it being TRUE: `actions/checkout@df4cb1c...` here
// really is v6.0.3, and this exact line sat labelled `# v6` (indistinguishable
// from a routine same-line bump) until this file. The offline half enforced
// here can only check the comment's *shape*; scripts/verify-pins.sh is the
// networked half that checks the comment against the real tag.
func TestActionsArePinnedToSHAs(t *testing.T) {
	entries, err := os.ReadDir("../../.github/workflows")
	if err != nil {
		t.Fatalf("read workflows dir: %v", err)
	}
	// A local `uses: ./.github/...` is a path, not a registry ref — nothing to pin.
	pinned := regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}\s+#\s*v\d+\.\d+\.\d+\s*$`)
	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../../.github/workflows", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			trimmed = strings.TrimPrefix(trimmed, "- ")
			if !strings.HasPrefix(trimmed, "uses:") {
				continue
			}
			ref := strings.TrimSpace(strings.TrimPrefix(trimmed, "uses:"))
			if strings.HasPrefix(ref, "./") {
				continue
			}
			found++
			if !pinned.MatchString(ref) {
				t.Errorf("%s:%d: %q is not pinned to a full commit SHA with an exact vX.Y.Z comment.\n"+
					"A tag is mutable and a bare major (`# v6`) can't be checked against the SHA. Use:\n"+
					"    uses: owner/action@<40-hex-sha> # vX.Y.Z",
					e.Name(), i+1, ref)
			}
		}
	}
	if found == 0 {
		t.Error("no `uses:` lines found in .github/workflows — this test is asserting nothing; " +
			"check the parser against the current workflow layout")
	}
}

// TestDependabotCoversEveryAction is the other half of pinning to SHAs.
//
// A pin closes the mutable-tag hole but opens a staleness one: a SHA never moves,
// including past a security fix, and unlike `@v6` nothing updates it for you.
// So pinning is only safe if something bumps the pins — here, Dependabot.
//
// The check that matters is coverage: an ecosystem entry that doesn't match an
// action leaves that action unmanaged, silently.
func TestDependabotCoversEveryAction(t *testing.T) {
	data, err := os.ReadFile("../../.github/dependabot.yml")
	if err != nil {
		t.Fatalf("read dependabot.yml: %v (CI's actions are pinned to SHAs; without "+
			"Dependabot nothing ever bumps them)", err)
	}
	var cfg struct {
		Version int `yaml:"version"`
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Directory string `yaml:"directory"`
			Groups    map[string]struct {
				Patterns []string `yaml:"patterns"`
			} `yaml:"groups"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("dependabot.yml is not valid YAML: %v", err)
	}
	if cfg.Version != 2 {
		t.Errorf("dependabot.yml version = %d, want 2 (v1 is unsupported)", cfg.Version)
	}

	var patterns []string
	ok := false
	for _, u := range cfg.Updates {
		if u.Ecosystem != "github-actions" {
			continue
		}
		ok = true
		if u.Directory != "/" {
			t.Errorf("the github-actions entry watches %q; workflows live in /.github/workflows, "+
				"which Dependabot finds via directory \"/\"", u.Directory)
		}
		for _, g := range u.Groups {
			patterns = append(patterns, g.Patterns...)
		}
	}
	if !ok {
		t.Fatal("dependabot.yml has no `github-actions` entry, so the SHA-pinned " +
			"actions in .github/workflows are never bumped")
	}

	for _, action := range workflowActions(t) {
		matched := false
		for _, p := range patterns {
			if globMatch(p, action) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s is not matched by any Dependabot group pattern %v, so it would "+
				"open its own PR outside the group (or be missed). Widen the pattern.",
				action, patterns)
		}
	}
}

// workflowStep returns the YAML block for the step named name: from its "- name:"
// line up to the next line at the same indentation starting a new list item.
func workflowStep(yaml, name string) (string, bool) {
	lines := strings.Split(yaml, "\n")
	start := -1
	var indent string
	for i, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), "name: "+name) &&
			strings.HasPrefix(strings.TrimSpace(line), "- ") {
			start = i
			indent = line[:strings.Index(line, "- ")]
			break
		}
	}
	if start < 0 {
		return "", false
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], indent+"- ") {
			return strings.Join(lines[start:i], "\n"), true
		}
	}
	return strings.Join(lines[start:], "\n"), true
}

// workflowActions returns the deduplicated owner/name of every registry action
// used by the workflows (local `./...` refs excluded — nothing to update).
func workflowActions(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../../.github/workflows")
	if err != nil {
		t.Fatalf("read workflows dir: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("../../.github/workflows", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimPrefix(strings.TrimSpace(line), "- ")
			if !strings.HasPrefix(trimmed, "uses:") {
				continue
			}
			ref := strings.TrimSpace(strings.TrimPrefix(trimmed, "uses:"))
			if strings.HasPrefix(ref, "./") {
				continue
			}
			name, _, _ := strings.Cut(ref, "@")
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no actions in .github/workflows — this test would assert nothing; " +
			"check the parser against the current workflow layout")
	}
	return out
}

// globMatch implements the only wildcard Dependabot patterns use: `*`, matching
// any run of characters (including `/`, so `*` alone matches everything).
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

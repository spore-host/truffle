package hygiene

import (
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestMakefileIsWellFormed: the Makefile parses cleanly, every target dry-runs,
// and `make help` lists exactly the targets that exist.
//
// Make does not fail on the mistakes a bad merge leaves behind. In #180 the
// nitro-census block was pasted twice into the middle of the
// check-release-version help comment (#197). A target defined twice is only a
// warning, printed on every run and then ignored by everyone. A `## ` help line
// that loses its prefix becomes a rule whose prerequisites are the words of the
// sentence. Nothing complains about it until someone runs that target, and then
// it fails with "No rule to make target `Release'". So this test asks make about
// each target directly. Targets come from the Makefile itself, so a newly added
// target is covered with no edit here.
func TestMakefileIsWellFormed(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("make is not on PATH in CI; the Makefile gate cannot run")
		}
		t.Skip("make not on PATH")
	}
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	targets := makefileTargets(string(data))
	if len(targets) == 0 {
		t.Fatal("found no targets in the Makefile; this test would assert nothing. " +
			"Check makefileTargets against the current Makefile layout")
	}

	// The duplicate-target warnings come from parsing, so every invocation prints
	// the same ones. Check them once, on `-pn`, which parses and prints the whole
	// database. Then dry-run each target, which catches a rule that names a
	// prerequisite nothing defines.
	checkNoOverrides(t, runMake(t, "-pn"))
	for _, target := range targets {
		runMake(t, "-n", target)
	}

	// Help entries: one `## <target>: <description>` line per target. Text that
	// continues a description goes in a plain `#` comment.
	helpLine := regexp.MustCompile(`^## ([A-Za-z0-9_.-]+): \S`)
	defined := map[string]bool{}
	for _, tg := range targets {
		defined[tg] = true
	}
	documented := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "##") {
			continue
		}
		m := helpLine.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("Makefile:%d: %q is a `##` line but not `## <target>: <description>`; "+
				"`make help` prints every `##` line, so use a plain `#` for continuation text", i+1, line)
			continue
		}
		if !defined[m[1]] {
			t.Errorf("Makefile:%d: `make help` documents %q, but the Makefile defines no such target", i+1, m[1])
		}
		if documented[m[1]] {
			t.Errorf("Makefile:%d: %q has more than one `##` help line", i+1, m[1])
		}
		documented[m[1]] = true
	}
	for _, tg := range targets {
		if !documented[tg] {
			t.Errorf("target %q has no `## %s: <description>` line, so `make help` does not list it", tg, tg)
		}
	}

	out := runMake(t, "help")
	for tg := range documented {
		if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(tg) + `\s`).MatchString(out) {
			t.Errorf("`make help` does not list %q:\n%s", tg, out)
		}
	}
}

// makefileTargets returns the explicit targets named at the start of a rule line,
// excluding special targets (.PHONY) and variable assignments (`X := y`).
func makefileTargets(makefile string) []string {
	rule := regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*:([^=]|$)`)
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(makefile, "\n") {
		if m := rule.FindStringSubmatch(line); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// checkNoOverrides fails if make warned about a recipe defined twice. GNU make
// 3.81 (the macOS default) says "commands" where 4.x says "recipe", so both are
// matched.
func checkNoOverrides(t *testing.T, out string) {
	t.Helper()
	for _, w := range []string{
		"warning: overriding recipe", "warning: ignoring old recipe",
		"warning: overriding commands", "warning: ignoring old commands",
	} {
		if strings.Contains(out, w) {
			t.Errorf("the Makefile defines a target more than once; make ignores all but the last definition:\n%s",
				grepLines(out, "warning:"))
			return
		}
	}
}

func runMake(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("make", append([]string{"-C", "../.."}, args...)...)
	// Run as a top-level make even when this test itself runs under `make test`.
	// Otherwise inherited MAKEFLAGS (jobserver fds, -n, -k) change the result.
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); k == "MAKEFLAGS" || k == "MFLAGS" || k == "MAKELEVEL" || k == "MAKEOVERRIDES" {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("make %s failed: %v\n%s", strings.Join(args, " "), err, grepLines(string(out), "make"))
	}
	return string(out)
}

// grepLines keeps the lines of s containing substr. A `make -pn` dump runs to
// thousands of lines, and only the diagnostics are useful in a failure message.
func grepLines(s, substr string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, substr) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

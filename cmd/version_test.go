package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/spore-host/libs/update"
)

func TestRenderUpdateNotice(t *testing.T) {
	cases := []struct {
		name     string
		res      *update.Result
		current  string
		contains string
	}{
		{"nil → couldn't check", nil, "0.52.0", "couldn't check"},
		{
			"newer available → upgrade line",
			&update.Result{CurrentVersion: "0.52.0", LatestVersion: "0.52.1", UpdateURL: "https://example.test/v0.52.1"},
			"0.52.0",
			"A newer version is available: 0.52.0 → 0.52.1",
		},
		{
			"on latest → reassurance",
			&update.Result{CurrentVersion: "0.52.1", LatestVersion: "0.52.1"},
			"0.52.1",
			"latest version",
		},
		{
			// The dev branch takes precedence over any result, including one that
			// claims an update. libs parses "dev" as 0.0.0, so a dev build would
			// otherwise be told to upgrade on every single command — even when it's
			// built from a commit AHEAD of the newest release, which is the normal
			// state while developing.
			"dev build → no comparison, even with a result in hand",
			&update.Result{CurrentVersion: "dev", LatestVersion: "0.52.1", UpdateURL: "https://example.test/v0.52.1"},
			"dev",
			"development build",
		},
		{
			"dirty dev build → no comparison",
			nil, "dev+dirty", "development build",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderUpdateNotice(tc.res, tc.current)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("renderUpdateNotice(_, %q) = %q, want it to contain %q", tc.current, got, tc.contains)
			}
		})
	}
}

// TestVersionHasNoHardcodedDefault is the regression guard for the actual bug
// (#121): cmd.Version was a hand-written "0.1.0" that nothing ever updated, so
// every source build reported it as fact no matter how many releases had
// shipped. The variable must stay empty in source, with the real value injected
// at release time and the build stamp as the fallback.
//
// Asserting on the variable rather than on version() is deliberate: version()
// resolves to a pseudo-version when the test binary is built inside a checkout,
// which would mask a reintroduced default.
func TestVersionHasNoHardcodedDefault(t *testing.T) {
	if Version != "" {
		t.Errorf("cmd.Version = %q; it must be empty in source. A default here is a "+
			"second source of truth that nothing updates — that is how it stayed at "+
			"0.1.0 across dozens of releases. The release injects it via -X (see "+
			".goreleaser.yaml) and non-release builds fall back to pkg/buildinfo.", Version)
	}
	for name, v := range map[string]string{"GitCommit": GitCommit, "BuildDate": BuildDate} {
		if v != "" {
			t.Errorf("cmd.%s = %q; it must be empty so versionDetail can tell "+
				"'not injected' from an injected placeholder", name, v)
		}
	}
}

// TestVersionIsNeverEmpty: whatever the build, the reported version must be
// something. An empty version banner is a worse failure than a dev marker.
func TestVersionIsNeverEmpty(t *testing.T) {
	if v := version(); strings.TrimSpace(v) == "" {
		t.Error("version() is empty")
	}
}

func TestVersionDetailFallsBackThroughToUnknown(t *testing.T) {
	// Injected values win.
	t.Run("injected wins", func(t *testing.T) {
		defer restore(&GitCommit, GitCommit)()
		defer restore(&BuildDate, BuildDate)()
		GitCommit, BuildDate = "abc1234", "2026-08-02T00:00:00Z"
		commit, date := versionDetail()
		if commit != "abc1234" || date != "2026-08-02T00:00:00Z" {
			t.Errorf("versionDetail() = (%q, %q), want the injected values", commit, date)
		}
	})

	// With nothing injected, the values come from the build stamp — or "unknown"
	// if the binary carries no VCS metadata. Never empty either way, because an
	// empty field in the version banner reads as a broken binary.
	t.Run("never empty", func(t *testing.T) {
		commit, date := versionDetail()
		if commit == "" || date == "" {
			t.Errorf("versionDetail() = (%q, %q); neither may be empty", commit, date)
		}
	})
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b"}, "a"},
		{[]string{"", "b"}, "b"},
		{[]string{"   ", "b"}, "b"}, // whitespace is empty: an -X fed a blank template yields this
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := firstNonEmpty(tc.in...); got != tc.want {
			t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGoreleaserInjectsTheVersionVariables checks, in ordinary CI, that
// .goreleaser.yaml still names the variables this file declares.
//
// The failure it prevents is silent by construction: `go build -ldflags "-X
// pkg.Missing=1"` does not error, so renaming cmd.Version (or the module) while
// leaving the ldflag alone builds, releases, and publishes binaries that report
// "dev" — with nothing anywhere saying why. A release guard script (if added,
// #121) would catch it at tag time by building and asking the binary; this
// catches it at PR time, which is where the rename happens.
func TestGoreleaserInjectsTheVersionVariables(t *testing.T) {
	data, err := os.ReadFile("../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	cfg := string(data)

	for _, want := range []string{
		"-X github.com/spore-host/truffle/cmd.Version={{.Version}}",
		"-X github.com/spore-host/truffle/cmd.GitCommit={{.Commit}}",
		"-X github.com/spore-host/truffle/cmd.BuildDate={{.Date}}",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf(".goreleaser.yaml is missing the ldflag %q — released binaries "+
				"would report %q instead of the tag", want, "dev")
		}
	}
}

// restore returns a func that resets *field to its original value, for tests
// that mutate a package-level var. Deferred immediately after capturing the
// original so cleanup happens even if the test fails partway through.
func restore(field *string, original string) func() {
	return func() { *field = original }
}

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/libs/update"
	"github.com/spore-host/truffle/pkg/buildinfo"
)

// Version is the release version, injected at build time by GoReleaser
// (-X .../cmd.Version={{.Version}}).
//
// The zero value is deliberately empty rather than a hand-written number. A
// hardcoded default is a second source of truth that nothing updates: this used
// to say "0.1.0" no matter how many releases had shipped, so every source
// build — and anything reading `truffle version` — reported a release that was
// wrong by construction. An empty value instead falls back to the build
// metadata Go stamps automatically (see pkg/buildinfo), which cannot go stale
// because nobody maintains it (#121).
var (
	Version   = ""
	GitCommit = ""
	BuildDate = ""
)

// version reports the running binary's version: the value injected at release
// time, else what the Go toolchain stamped in. See pkg/buildinfo for why there
// is no hardcoded default.
func version() string {
	return buildinfo.Version(Version)
}

// versionDetail returns the commit and build date to display, each falling back
// to the toolchain's stamp and then to "unknown".
func versionDetail() (commit, date string) {
	revision, buildTime := buildinfo.Revision()
	commit = firstNonEmpty(GitCommit, revision, "unknown")
	date = firstNonEmpty(BuildDate, buildTime, "unknown")
	return commit, date
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Display version, build date, and git commit information for truffle.`,
	Run: func(cmd *cobra.Command, args []string) {
		v := version()
		commit, date := versionDetail()
		fmt.Printf("%s Truffle - AWS EC2 Instance Type Finder\n\n", i18n.Emoji("mushroom"))
		fmt.Printf("Version:    %s\n", v)
		fmt.Printf("Git Commit: %s\n", commit)
		fmt.Printf("Build Date: %s\n", date)
		fmt.Printf("\nProject:    https://spore.host\n")

		// Explicit, user-initiated check — report whether a newer release exists.
		// Skipped for a dev build: there is no release number to compare, and
		// libs' semver parser reads "dev" as 0.0.0, which would otherwise nag on
		// every command — including one built from a commit ahead of the last
		// release.
		var res *update.Result
		if !buildinfo.IsDev(v) {
			res = update.CheckNow("truffle", v)
		}
		fmt.Print(renderUpdateNotice(res, v))
	},
}

// renderUpdateNotice formats the on-demand update check for the version command:
// a dev build → say the comparison is meaningless rather than fake an answer; a
// nil result (GitHub unreachable) → "couldn't check"; a newer release → an
// upgrade line; otherwise → "on the latest version". Pure, so it's unit-tested
// without a network call.
func renderUpdateNotice(res *update.Result, current string) string {
	switch {
	case buildinfo.IsDev(current):
		return "\n(development build — not comparing against releases)\n"
	case res == nil:
		return "\n(couldn't check for updates)\n"
	case res.HasUpdate():
		return fmt.Sprintf("\n⬆️  A newer version is available: %s → %s\n    %s\n",
			res.CurrentVersion, res.LatestVersion, res.UpdateURL)
	default:
		return "\n✓ You're on the latest version.\n"
	}
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

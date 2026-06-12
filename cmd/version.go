package cmd

import (
	"fmt"

	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/libs/update"
	"github.com/spf13/cobra"
)

var (
	Version   = "0.1.0"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Display version, build date, and git commit information for truffle.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("%s Truffle - AWS EC2 Instance Type Finder\n\n", i18n.Emoji("mushroom"))
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Git Commit: %s\n", GitCommit)
		fmt.Printf("Build Date: %s\n", BuildDate)
		fmt.Printf("\nProject:    https://spore.host\n")

		// Explicit, user-initiated check — report whether a newer release exists.
		if res := update.CheckNow("truffle", Version); res == nil {
			fmt.Printf("\n(couldn't check for updates)\n")
		} else if res.HasUpdate() {
			fmt.Printf("\n⬆️  A newer version is available: %s → %s\n    %s\n",
				res.CurrentVersion, res.LatestVersion, res.UpdateURL)
		} else {
			fmt.Printf("\n✓ You're on the latest version.\n")
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

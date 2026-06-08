package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spore-host/libs/i18n"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	outputFormat string
	noColor      bool
	regions      []string
	verbose      bool

	// i18n and accessibility flags
	flagLang          string
	flagNoEmoji       bool
	flagAccessibility bool
)

var rootCmd = &cobra.Command{
	Use: "truffle",
	// Short and Long will be set after i18n initialization
}

var i18nInitialized = false

func Execute() {
	// Snapshot --lang value before Execute() parses flags into the bound slice.
	// We only need --lang for early i18n init; reading it via Lookup avoids
	// double-appending to StringSliceVar-bound slices (see #19).
	for i, arg := range os.Args[1:] {
		if arg == "--lang" && i+1 < len(os.Args[1:]) {
			flagLang = os.Args[i+2]
			break
		}
		if len(arg) > 7 && arg[:7] == "--lang=" {
			flagLang = arg[7:]
			break
		}
	}
	ensureI18nInitialized()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Set PersistentPreRunE to initialize i18n after flag parsing
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		ensureI18nInitialized()

		// Merge --region (singular alias) into --regions for backward compatibility
		regionFlag := rootCmd.PersistentFlags().Lookup("region")
		if regionFlag != nil && regionFlag.Changed {
			regionValues, err := rootCmd.PersistentFlags().GetStringSlice("region")
			if err == nil && len(regionValues) > 0 {
				for _, r := range regionValues {
					found := false
					for _, existing := range regions {
						if existing == r {
							found = true
							break
						}
					}
					if !found {
						regions = append(regions, r)
					}
				}
			}
		}

		// Dedup regions (cobra StringSliceVar can accumulate duplicates)
		if len(regions) > 1 {
			seen := make(map[string]bool, len(regions))
			deduped := regions[:0]
			for _, r := range regions {
				if !seen[r] {
					seen[r] = true
					deduped = append(deduped, r)
				}
			}
			regions = deduped
		}

		return nil
	}

	// Add i18n and accessibility flags
	rootCmd.PersistentFlags().StringVar(&flagLang, "lang", "", "Language for output (en, es, fr, de, ja, pt)")
	rootCmd.PersistentFlags().BoolVar(&flagNoEmoji, "no-emoji", false, "Disable emoji in output")
	rootCmd.PersistentFlags().BoolVar(&flagAccessibility, "accessibility", false, "Enable accessibility mode (implies --no-emoji)")

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json, yaml, csv)")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colorized output")
	rootCmd.PersistentFlags().StringSliceVarP(&regions, "regions", "r", []string{}, "Filter by specific regions (comma-separated)")
	rootCmd.PersistentFlags().StringSlice("region", []string{}, "Alias for --regions")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	// Enable shell completion for all supported shells
	rootCmd.CompletionOptions.DisableDefaultCmd = false
	rootCmd.CompletionOptions.DisableDescriptions = false

	// Register completion for persistent flags
	_ = rootCmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"table", "json", "yaml", "csv"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = rootCmd.RegisterFlagCompletionFunc("regions", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return completeRegion(cmd, args, toComplete)
	})
}

func ensureI18nInitialized() {
	if i18nInitialized {
		return
	}
	initI18n()
	i18nInitialized = true
}

func initI18n() {
	// Initialize i18n with configuration from flags
	cfg := i18n.Config{
		Language:          flagLang,
		Verbose:           false,
		AccessibilityMode: flagAccessibility,
		NoEmoji:           flagNoEmoji,
	}

	if err := i18n.Init(cfg); err != nil {
		log.Printf("Warning: failed to initialize i18n: %v", err)
		// Continue with default English
	}

	// Set command descriptions after i18n is initialized
	updateCommandDescriptions()
}

func updateCommandDescriptions() {
	// Root command
	rootCmd.Short = i18n.T("truffle.root.short")
	rootCmd.Long = i18n.T("truffle.root.long")

	// Search command — Short/Long are set directly on searchCmd since they
	// reference pattern syntax that must stay in sync with the code.
	if cmd, _, err := rootCmd.Find([]string{"search"}); err == nil && cmd != nil {
		cmd.Long = "Search for instance types across AWS regions.\n\nSupports glob patterns (m7i*, c7?) and regex (c[6-8]i\\.large, (p4d|p5)\\..*) for flexible matching."
	}

	// Capacity command
	if cmd, _, err := rootCmd.Find([]string{"capacity"}); err == nil && cmd != nil {
		cmd.Short = i18n.T("truffle.capacity.short")
		cmd.Long = i18n.T("truffle.capacity.long")
	}

	// Spot command
	if cmd, _, err := rootCmd.Find([]string{"spot"}); err == nil && cmd != nil {
		cmd.Short = i18n.T("truffle.spot.short")
		cmd.Long = i18n.T("truffle.spot.long")
	}
}

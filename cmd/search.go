package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spore-host/libs/i18n"
	"github.com/spf13/cobra"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/output"
	"github.com/spore-host/truffle/pkg/progress"
)

var (
	skipAZs         bool
	architecture    string
	minVCPUs        int
	minMemory       float64
	instanceFamily  string
	searchPickFirst bool
	searchShowPrice bool
	timeout         time.Duration
)

var searchCmd = &cobra.Command{
	Use:   "search [instance-type-pattern]",
	Short: "Search by instance type pattern (glob: 'm7i*' or regex: 'c[6-8]i\\.large')",
	Args:  cobra.ExactArgs(1),
	RunE:  runSearch,
}

func init() {
	rootCmd.AddCommand(searchCmd)

	searchCmd.Flags().BoolVar(&skipAZs, "skip-azs", false, "Skip availability zone lookup (faster but less detailed)")
	searchCmd.Flags().StringVar(&architecture, "architecture", "", "Filter by architecture (x86_64, arm64, i386)")
	searchCmd.Flags().IntVar(&minVCPUs, "min-vcpu", 0, "Minimum number of vCPUs")
	searchCmd.Flags().Float64Var(&minMemory, "min-memory", 0, "Minimum memory in GiB")
	searchCmd.Flags().StringVar(&instanceFamily, "family", "", "Filter by instance family (e.g., m5, c5)")
	searchCmd.Flags().BoolVar(&searchPickFirst, "pick-first", false, "Output only the top result's instance type (useful for piping to spawn)")
	searchCmd.Flags().BoolVar(&searchShowPrice, "show-price", false, "Show on-demand pricing (uses static pricing data)")
	searchCmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")

	// Register completion for instance type argument
	searchCmd.ValidArgsFunction = completeInstanceType

	// Register completion for flags
	_ = searchCmd.RegisterFlagCompletionFunc("architecture", completeArchitecture)
	_ = searchCmd.RegisterFlagCompletionFunc("family", completeInstanceFamily)
}

func runSearch(cmd *cobra.Command, args []string) error {
	pattern := args[0]

	// Convert pattern to regex: if the pattern contains regex metacharacters
	// (brackets, +, unescaped dots not followed by *), treat it as a regex.
	// Otherwise treat it as a glob (only * and ? are wildcards).
	regexPattern := patternToRegex(pattern)
	matcher, err := regexp.Compile(regexPattern)
	if err != nil {
		return i18n.Te("truffle.search.error.invalid_pattern", err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("magnifying_glass"), i18n.Tf("truffle.search.searching", map[string]interface{}{
			"Pattern": pattern,
		}))
		if len(regions) > 0 {
			fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("pushpin"), i18n.Tf("truffle.search.filtering_regions", map[string]interface{}{
				"Regions": strings.Join(regions, ", "),
			}))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Initialize AWS client
	awsClient, err := aws.NewClient(ctx)
	if err != nil {
		return i18n.Te("error.aws_client_init", err)
	}

	// Get regions to search
	// If no regions specified, auto-detect enabled regions (respects SCPs)
	searchRegions := regions
	if len(searchRegions) == 0 {
		if verbose {
			fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("globe"), i18n.T("truffle.search.fetching_regions"))
		}
		searchRegions, err = awsClient.GetEnabledRegions(ctx)
		if err != nil {
			return i18n.Te("truffle.search.error.get_regions_failed", err)
		}

		// Warn about searching all regions (can be slow)
		if len(searchRegions) > 10 && outputFormat == "table" {
			fmt.Fprintf(os.Stderr, "%s Searching across all %d enabled regions (this may take a while)\n",
				i18n.Emoji("warning"), len(searchRegions))
			fmt.Fprintf(os.Stderr, "   Tip: Use --regions to limit search (e.g., --regions us-east-1,us-west-2)\n\n")
		}
	}

	// Show spinner for non-verbose mode
	var spinner *progress.Spinner
	if !verbose && outputFormat == "table" {
		msg := fmt.Sprintf("Searching across %d %s...", len(searchRegions), pluralize(len(searchRegions), "region", "regions"))
		spinner = progress.NewSpinner(os.Stderr, msg)
		spinner.Start()
	} else if verbose {
		fmt.Fprintf(os.Stderr, "%s Searching across %d %s\n", i18n.Emoji("magnifying_glass_tilted"), len(searchRegions), pluralize(len(searchRegions), "region", "regions"))
	}

	// Search for instance types
	results, err := awsClient.SearchInstanceTypes(ctx, searchRegions, matcher, aws.FilterOptions{
		IncludeAZs:     !skipAZs, // AZs included by default
		Architecture:   architecture,
		MinVCPUs:       minVCPUs,
		MinMemory:      minMemory,
		InstanceFamily: instanceFamily,
		Verbose:        verbose,
	})

	if spinner != nil {
		spinner.Stop()
	}

	if err != nil {
		return i18n.Te("truffle.search.error.search_failed", err)
	}

	// Sort results for consistent output
	sort.Slice(results, func(i, j int) bool {
		if results[i].InstanceType != results[j].InstanceType {
			return results[i].InstanceType < results[j].InstanceType
		}
		return results[i].Region < results[j].Region
	})

	if len(results) == 0 {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "%s Request timed out after %s. Results may be incomplete.\n    Try increasing --timeout (default: 5m0s) or narrowing --regions.\n", i18n.Symbol("warning"), timeout)
		}
		fmt.Println(i18n.T("truffle.search.no_results"))
		return nil
	}

	// Populate on-demand pricing if requested
	if searchShowPrice {
		for idx := range results {
			price, _ := awsClient.OnDemandPrice(ctx, results[idx].InstanceType, results[idx].Region)
			results[idx].OnDemandPrice = price
		}
	}

	// --pick-first: output just the instance type of the top result and exit
	if searchPickFirst {
		fmt.Println(results[0].InstanceType)
		return nil
	}

	// Output results
	printer := output.NewPrinter(!noColor)
	switch outputFormat {
	case "json":
		return printer.PrintJSON(results)
	case "yaml":
		return printer.PrintYAML(results)
	case "csv":
		return printer.PrintCSV(results)
	case "table":
		return printer.PrintTable(results, !skipAZs, searchShowPrice)
	default:
		return i18n.Te("truffle.search.error.unsupported_format", nil, map[string]interface{}{
			"Format": outputFormat,
		})
	}
}

// patternToRegex converts a user pattern to a regex. If the pattern contains
// regex metacharacters ([, ], +, \d, \w, unanchored ^/$), it's treated as a
// regex directly. Otherwise it's treated as a glob where * and ? are wildcards.
func patternToRegex(pattern string) string {
	if looksLikeRegex(pattern) {
		// Already a regex — just anchor it if not already anchored
		if !strings.HasPrefix(pattern, "^") {
			pattern = "^" + pattern
		}
		if !strings.HasSuffix(pattern, "$") {
			pattern = pattern + "$"
		}
		return pattern
	}
	return wildcardToRegex(pattern)
}

func looksLikeRegex(pattern string) bool {
	for _, indicator := range []string{"[", "]", "(", ")", "+", "\\d", "\\w", "\\s", "|"} {
		if strings.Contains(pattern, indicator) {
			return true
		}
	}
	return false
}

func wildcardToRegex(pattern string) string {
	// Escape special regex characters except * and ?
	pattern = regexp.QuoteMeta(pattern)
	// Replace wildcards
	pattern = strings.ReplaceAll(pattern, `\*`, ".*")
	pattern = strings.ReplaceAll(pattern, `\?`, ".")
	// Anchor the pattern
	return "^" + pattern + "$"
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

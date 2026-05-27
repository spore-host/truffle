package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spore-host/libs/catalog"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
	"github.com/spore-host/truffle/pkg/output"
	"github.com/spore-host/truffle/pkg/progress"
	"github.com/spf13/cobra"
)

var (
	findSkipAZs   bool
	findShowQuery bool
	findTimeout   time.Duration
	findApp       string // --app flag: application name from catalog
	findExact     bool   // --exact flag: match exact vCPU and memory instead of minimum
)

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Find instances using natural language (e.g. 'nvidia h100 8gpu', 'amd epyc genoa')",
	Long: `Find EC2 instance types using natural language queries.

Understands:
  - CPU vendors: intel, amd, graviton
  - Processor code names: ice lake, milan, sapphire rapids, genoa
  - GPU types: a100, v100, h100, t4, l4, inferentia, trainium
  - Sizes: tiny, small, medium, large, huge
  - Specs: 8 cores, 32gb, 4 gpus
  - Architecture: x86_64, arm64
  - Network: efa, 10gbps, 25gbps, 50gbps, 100gbps, 200gbps, 400gbps

Examples:
  truffle find graviton
  truffle find "ice lake"
  truffle find "amd 16 cores"
  truffle find a100
  truffle find "graviton large"
  truffle find "intel gpu"
  truffle find "milan 64gb"
  truffle find "sapphire rapids 32 cores"
  truffle find "inferentia"
  truffle find "efa graviton"
  truffle find "100gbps intel"
  truffle find "h100 efa"`,
	Args: cobra.ArbitraryArgs, // 0 args allowed when --app is used
	RunE: runFind,
}

func init() {
	rootCmd.AddCommand(findCmd)

	findCmd.Flags().BoolVar(&findSkipAZs, "skip-azs", false, "Skip availability zone lookup (faster)")
	findCmd.Flags().BoolVar(&findShowQuery, "show-query", false, "Show parsed query details")
	findCmd.Flags().DurationVar(&findTimeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")
	findCmd.Flags().StringVar(&findApp, "app", "", "Application name from catalog (e.g. paraview, igv)")
	findCmd.Flags().BoolVar(&findExact, "exact", false, "Match exact vCPU and memory values instead of minimum")
}

func runFind(cmd *cobra.Command, args []string) error {
	// Join args to handle multi-word queries
	queryStr := strings.Join(args, " ")

	// --app flag injects the app name into the query string so it flows
	// through the normal parse pipeline as a TokenApp.
	if findApp != "" {
		if queryStr != "" {
			queryStr = findApp + " " + queryStr
		} else {
			queryStr = findApp
		}
	}

	if queryStr == "" {
		return fmt.Errorf("query or --app required")
	}

	// Parse query
	query, err := find.ParseQuery(queryStr)
	if err != nil {
		return fmt.Errorf("failed to parse query: %w", err)
	}

	// Apply --exact flag
	if findExact {
		query.ExactMatch = true
	}

	// Show parsed query if requested
	if findShowQuery || verbose {
		printParsedQuery(query)
	}

	// Build search criteria
	criteria, err := query.BuildCriteria()
	if err != nil {
		return fmt.Errorf("failed to build search criteria: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()

	// Create AWS client
	client, err := aws.NewClient(ctx)
	if err != nil {
		return i18n.Te("error.aws_client_init", err)
	}

	// Resolve regions
	searchRegions := regions
	if len(searchRegions) == 0 {
		if verbose {
			fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("globe"), i18n.T("truffle.search.fetching_regions"))
		}
		searchRegions, err = client.GetEnabledRegions(ctx)
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
		msg := fmt.Sprintf("Finding '%s' across %d region(s)", queryStr, len(searchRegions))
		spinner = progress.NewSpinner(os.Stderr, msg)
		spinner.Start()
	} else if verbose {
		fmt.Fprintf(os.Stderr, "%s Finding across %d region(s)\n", i18n.Emoji("magnifying_glass_tilted"), len(searchRegions))
	}

	// Execute search
	results, err := client.SearchInstanceTypes(ctx, searchRegions, criteria.InstanceTypePattern, criteria.FilterOptions)

	if spinner != nil {
		spinner.Stop()
	}

	if err != nil {
		return i18n.Te("truffle.search.error.search_failed", err)
	}

	// Sort results
	sort.Slice(results, func(i, j int) bool {
		if results[i].InstanceType != results[j].InstanceType {
			return results[i].InstanceType < results[j].InstanceType
		}
		return results[i].Region < results[j].Region
	})

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No instances match your query")
		printSuggestions(query)
		return nil
	}

	// Add match explanations
	enrichedResults := make([]find.FindResult, 0, len(results))
	for _, r := range results {
		enrichedResults = append(enrichedResults, find.FindResult{
			InstanceTypeResult: r,
			MatchReasons:       find.ExplainMatch(r, query),
		})
	}

	// Print results
	return printFindResults(enrichedResults)
}

func printParsedQuery(query *find.ParsedQuery) {
	fmt.Fprintf(os.Stderr, "%s Parsed query:\n", i18n.Emoji("magnifying_glass"))

	if len(query.Apps) > 0 {
		for _, appName := range query.Apps {
			if entry, ok := catalog.Lookup(appName); ok {
				fmt.Fprintf(os.Stderr, "   App: %s — %s\n", entry.Name, entry.Description)
				fmt.Fprintf(os.Stderr, "        Families: %s  Min: %d vCPU / %d GiB RAM\n",
					strings.Join(entry.InstanceFamilies, ", "), entry.MinVCPUs, entry.MinMemoryGiB)
			}
		}
	}
	if len(query.Vendors) > 0 {
		fmt.Fprintf(os.Stderr, "   Vendor: %s\n", strings.Join(query.Vendors, ", "))
	}
	if len(query.Processors) > 0 {
		fmt.Fprintf(os.Stderr, "   Processor: %s\n", strings.Join(query.Processors, ", "))
	}
	if len(query.GPUs) > 0 {
		fmt.Fprintf(os.Stderr, "   GPU: %s\n", strings.Join(query.GPUs, ", "))
	}
	if len(query.Sizes) > 0 {
		fmt.Fprintf(os.Stderr, "   Size: %s\n", strings.Join(query.Sizes, ", "))
	}
	if query.MinVCPU > 0 {
		fmt.Fprintf(os.Stderr, "   Min vCPUs: %d\n", query.MinVCPU)
	}
	if query.MinMemory > 0 {
		fmt.Fprintf(os.Stderr, "   Min Memory: %.0f GiB\n", query.MinMemory)
	}
	if query.Architecture != "" {
		fmt.Fprintf(os.Stderr, "   Architecture: %s\n", query.Architecture)
	}
	if query.RequireEFA {
		fmt.Fprintf(os.Stderr, "   Network: EFA required\n")
	}
	if query.MinNetworkGbps > 0 {
		fmt.Fprintf(os.Stderr, "   Min Network: %d Gbps\n", query.MinNetworkGbps)
	}

	// Show resolved families
	families := query.ResolveInstanceFamilies()
	if len(families) > 0 {
		fmt.Fprintf(os.Stderr, "   Instance families: %s\n", strings.Join(families, ", "))
	}

	fmt.Fprintln(os.Stderr)
}

func printSuggestions(query *find.ParsedQuery) {
	fmt.Fprintln(os.Stderr, "\nSuggestions:")

	// Check for unknown tokens
	var unknownTokens []string
	for _, token := range query.RawTokens {
		if token.Type == find.TokenUnknown {
			unknownTokens = append(unknownTokens, token.Raw)
		}
	}

	if len(unknownTokens) > 0 {
		fmt.Fprintf(os.Stderr, "  - Unknown terms: %s\n", strings.Join(unknownTokens, ", "))
		fmt.Fprintln(os.Stderr, "  - Try: intel, amd, graviton, ice lake, milan, a100, v100, h100")
	}

	if len(query.Vendors) == 0 && len(query.Processors) == 0 && len(query.GPUs) == 0 {
		fmt.Fprintln(os.Stderr, "  - Specify a vendor (intel, amd, graviton)")
		fmt.Fprintln(os.Stderr, "  - Or a processor (ice lake, milan, sapphire rapids)")
		fmt.Fprintln(os.Stderr, "  - Or a GPU (a100, v100, h100, inferentia)")
	}

	if query.MinVCPU > 128 {
		fmt.Fprintln(os.Stderr, "  - Very high vCPU requirement may limit results")
	}

	if query.MinMemory > 1024 {
		fmt.Fprintln(os.Stderr, "  - Very high memory requirement may limit results")
	}
}

func printFindResults(results []find.FindResult) error {
	printer := output.NewPrinter(!noColor)

	switch outputFormat {
	case "json":
		return printer.PrintJSON(convertToInstanceTypeResults(results))
	case "yaml":
		return printer.PrintYAML(convertToInstanceTypeResults(results))
	case "csv":
		return printer.PrintCSV(convertToInstanceTypeResults(results))
	case "table":
		return printFindTable(results, printer)
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

func printFindTable(results []find.FindResult, printer *output.Printer) error {
	// For now, print standard table with match reasons in summary
	baseResults := convertToInstanceTypeResults(results)

	// Print match reasons summary if verbose
	if verbose && len(results) > 0 {
		fmt.Fprintln(os.Stderr, "\nMatch explanations:")
		for _, r := range results {
			if len(r.MatchReasons) > 0 {
				fmt.Fprintf(os.Stderr, "  %s: %s\n", r.InstanceType, strings.Join(r.MatchReasons, ", "))
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	return printer.PrintTable(baseResults, !findSkipAZs, true) // show on-demand price by default
}

func convertToInstanceTypeResults(findResults []find.FindResult) []aws.InstanceTypeResult {
	results := make([]aws.InstanceTypeResult, len(findResults))
	for i, r := range findResults {
		results[i] = r.InstanceTypeResult
	}
	return results
}

package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/catalog"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
	"github.com/spore-host/truffle/pkg/metadata"
	"github.com/spore-host/truffle/pkg/output"
	"github.com/spore-host/truffle/pkg/progress"
)

var (
	findSkipAZs       bool
	findShowQuery     bool
	findTimeout       time.Duration
	findApp           string // --app flag: application name from catalog
	findExact         bool   // --exact flag: match exact vCPU and memory instead of minimum
	findPickFirst     bool
	findService       string // --service flag: "ec2" (default) or "sagemaker"
	findShowQuota     bool   // --show-quota flag: show per-type training-job quota (SageMaker only)
	findShowPrice     bool   // --show-price flag: populate the on-demand price column
	findShowRealCores bool   // --show-real-cores flag: vCPU column also shows "/physicalCores" (#136)
	findShowMemPerCPU bool   // --show-mem-per-cpu flag: Memory column appends a per-physical-core figure (#137)
	findPriceUnit     string // --price-unit flag: hour (default), minute, or second (#138)
)

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Find instances by natural language, pattern, or specs",
	Long: `Find EC2 instance types using natural language, glob patterns, or regex.

Auto-detects query type:
  - Patterns: m7i*, c[6-8]i.large, g5.* → pattern matching
  - Natural language: "graviton 8 cores 32gb" → spec-based search

Understands:
  - CPU vendors: intel, amd, graviton, nvidia
  - Processors: emerald rapids, sapphire rapids, ice lake, genoa, turin, milan
  - GPUs: h200, h100, a100, b200, b300, l40s, l4, a10g, t4, rtx, inferentia, trainium
  - Instruction sets: avx2, avx-512, sve, sve2
  - Specs: 8 cores, 8 physical cores, 32gb, 4 gpus
  - Sizes: tiny, small, medium, large, huge
  - Architecture: x86_64, arm64
  - Network: efa, 10gbps, 25gbps, 50gbps, 100gbps, 200gbps, 400gbps
  - Sort hints: cheap/cheapest, fast/fastest, newest/latest

Examples:
  truffle find "m7i*"                         (glob pattern)
  truffle find "c[6-8]i.large"                (regex pattern)
  truffle find graviton                       (vendor search)
  truffle find "turin 32 cores 64gb" --exact  (exact spec match)
  truffle find "8 physical cores 32gb"        (physical core count)
  truffle find "cheap graviton 8 cores"       (sorted by price)
  truffle find nvidia                         (all NVIDIA GPU instances)
  truffle find "h100 efa"                     (GPU + network)
  truffle find avx-512                        (instruction-set search)
  truffle find "sve2 graviton"                (instruction set + vendor)`,
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
	findCmd.Flags().BoolVar(&findPickFirst, "pick-first", false, "Output only the top result's instance type (useful for piping to spawn)")
	findCmd.Flags().BoolVar(&findShowPrice, "show-price", false, "Show on-demand pricing (live AWS Price List; warns if it falls back to built-in data)")
	findCmd.Flags().StringVar(&findService, "service", "ec2", "Instance namespace to search: ec2 or sagemaker (ml.* types)")
	findCmd.Flags().BoolVar(&findShowQuota, "show-quota", false, "Show the per-type training-job quota (SageMaker only)")
	findCmd.Flags().BoolVar(&findShowRealCores, "show-real-cores", false, "Show physical CPU core count alongside vCPUs (format: vCPU/CPU, e.g. 96/48)")
	findCmd.Flags().BoolVar(&findShowMemPerCPU, "show-mem-per-cpu", false, "Show memory per physical core alongside total memory")
	findCmd.Flags().StringVar(&findPriceUnit, "price-unit", "hour", "Price display unit for --show-price: hour, minute, or second")
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

	service, err := validateServiceFlag(findService)
	if err != nil {
		return err
	}

	if _, err := validatePriceUnitFlag(findPriceUnit); err != nil {
		return err
	}

	// Auto-detect: if query looks like a pattern (glob or regex), route to
	// the pattern-matching path instead of NL parsing.
	if looksLikePattern(queryStr) {
		return runSearchWithPattern(queryStr, service)
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

	// Apply sort preference from qualitative keywords
	sortPref := query.SortPreference()
	if sortPref != find.SortDefault {
		sortLabel := map[find.SortPreference]string{
			find.SortCheapest:   "price (lowest first)",
			find.SortExpensive:  "price (highest first)",
			find.SortPerformant: "vCPUs (most first)",
			find.SortNewest:     "generation (newest first)",
		}
		fmt.Fprintf(os.Stderr, "Sorting by: %s\n", sortLabel[sortPref])
	}

	// Warn about remaining qualitative keywords that have no effect
	if quals := query.QualitativeTokens(); len(quals) > 0 {
		var unhandled []string
		for _, q := range quals {
			if _, ok := find.QualitativeSortMap[q]; !ok {
				unhandled = append(unhandled, q)
			}
		}
		if len(unhandled) > 0 {
			fmt.Fprintf(os.Stderr, "Note: %q ignored — no matching sort or filter.\n", strings.Join(unhandled, "\", \""))
		}
	}

	// Show parsed query if requested
	if findShowQuery || verbose {
		printParsedQuery(query)
	}

	// Build search criteria. IncludeAZs drives the search-time per-type AZ
	// lookup (expensive — one extra API call per matched type, truffle#141),
	// not just the table's AZ column — so --skip-azs must reach it here.
	criteria, err := query.BuildCriteria(!findSkipAZs)
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
		msg := fmt.Sprintf("Finding '%s' across %d %s", queryStr, len(searchRegions), pluralize(len(searchRegions), "region", "regions"))
		spinner = progress.NewSpinner(os.Stderr, msg)
		spinner.Start()
	} else if verbose {
		fmt.Fprintf(os.Stderr, "%s Finding across %d %s\n", i18n.Emoji("magnifying_glass_tilted"), len(searchRegions), pluralize(len(searchRegions), "region", "regions"))
	}

	// Execute search
	var results []aws.InstanceTypeResult
	if service == "sagemaker" {
		results, err = client.SearchSageMakerInstanceTypes(ctx, searchRegions, criteria.InstanceTypePattern, criteria.FilterOptions)
	} else {
		results, err = client.SearchInstanceTypes(ctx, searchRegions, criteria.InstanceTypePattern, criteria.FilterOptions)
	}

	if spinner != nil {
		spinner.Stop()
	}

	if err != nil {
		return i18n.Te("truffle.search.error.search_failed", err)
	}

	// Populate on-demand pricing. SageMaker ml.* types are priced under a
	// distinct offer (AmazonSageMaker) with a management premium, so they use a
	// separate pricer keyed on the ml.*-prefixed name.
	//
	// A rate that came from the embedded fallback table instead of the live Price
	// List is flagged once below rather than per row: it is a real published rate
	// but may be stale, and a reader comparing costs should know which numbers are
	// current (#114).
	staticPriced := 0
	for idx := range results {
		var price float64
		if service == "sagemaker" {
			price, _ = client.SageMakerPrice(ctx, results[idx].InstanceType, results[idx].Region)
		} else {
			var src aws.PriceSource
			price, src, _ = client.OnDemandPriceWithSource(ctx, results[idx].InstanceType, results[idx].Region)
			if src == aws.PriceSourceStatic {
				staticPriced++
			}
		}
		results[idx].OnDemandPrice = price
	}
	if staticPriced > 0 {
		fmt.Fprintf(os.Stderr, "%s On-demand price for %d of %d %s from truffle's built-in table, not the live AWS Price List — it may be out of date.\n",
			i18n.Symbol("warning"), staticPriced, len(results), pluralize(staticPriced, "type is", "types are"))
	}

	// Sort results based on qualitative preference or default (newest gen first)
	sort.Slice(results, func(i, j int) bool {
		switch sortPref {
		case find.SortCheapest:
			pi, pj := results[i].OnDemandPrice, results[j].OnDemandPrice
			if pi == 0 && pj != 0 {
				return false // push unknown prices to end
			}
			if pi != 0 && pj == 0 {
				return true
			}
			if pi != pj {
				return pi < pj
			}
		case find.SortExpensive:
			pi, pj := results[i].OnDemandPrice, results[j].OnDemandPrice
			if pi == 0 && pj != 0 {
				return false
			}
			if pi != 0 && pj == 0 {
				return true
			}
			if pi != pj {
				return pi > pj
			}
		case find.SortPerformant:
			if results[i].VCPUs != results[j].VCPUs {
				return results[i].VCPUs > results[j].VCPUs
			}
		case find.SortNewest:
			genI := instanceGeneration(results[i].InstanceType)
			genJ := instanceGeneration(results[j].InstanceType)
			if genI != genJ {
				return genI > genJ
			}
		default:
			genI := instanceGeneration(results[i].InstanceType)
			genJ := instanceGeneration(results[j].InstanceType)
			if genI != genJ {
				return genI > genJ
			}
		}
		if results[i].InstanceType != results[j].InstanceType {
			return results[i].InstanceType < results[j].InstanceType
		}
		return results[i].Region < results[j].Region
	})

	if len(results) == 0 {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "%s Request timed out after %s. Results may be incomplete.\n    Try increasing --timeout or narrowing --regions.\n", i18n.Symbol("warning"), findTimeout)
		}
		fmt.Fprintln(os.Stderr, "No instances match your query")
		printSuggestions(query)
		return nil
	}

	// --pick-first: output just the instance type of the top result and exit
	if findPickFirst {
		fmt.Println(results[0].InstanceType)
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
	if len(query.InstructionSets) > 0 {
		fmt.Fprintf(os.Stderr, "   Instruction set: %s\n", strings.Join(query.InstructionSets, ", "))
	}
	if len(query.Sizes) > 0 {
		fmt.Fprintf(os.Stderr, "   Size: %s\n", strings.Join(query.Sizes, ", "))
	}
	if query.MinVCPU > 0 {
		fmt.Fprintf(os.Stderr, "   Min vCPUs: %d\n", query.MinVCPU)
	}
	if query.MinPhysCores > 0 {
		fmt.Fprintf(os.Stderr, "   Min Physical Cores: %d\n", query.MinPhysCores)
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

	if len(query.Vendors) == 0 && len(query.Processors) == 0 && len(query.GPUs) == 0 && len(query.InstructionSets) == 0 {
		fmt.Fprintln(os.Stderr, "  - Specify a vendor (intel, amd, graviton)")
		fmt.Fprintln(os.Stderr, "  - Or a processor (ice lake, milan, sapphire rapids)")
		fmt.Fprintln(os.Stderr, "  - Or a GPU (a100, v100, h100, inferentia)")
		fmt.Fprintln(os.Stderr, "  - Or an instruction set (avx2, avx-512, sve, sve2)")
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

	// findService/findPriceUnit are validated in runFind before this path is
	// reached; the quota column is SageMaker-only.
	priceUnit, _ := validatePriceUnitFlag(findPriceUnit)
	opts := output.TableOptions{
		IncludeAZs:    !findSkipAZs,
		ShowPrice:     true, // show on-demand price by default
		ShowQuota:     findShowQuota && strings.EqualFold(findService, "sagemaker"),
		ShowRealCores: findShowRealCores,
		ShowMemPerCPU: findShowMemPerCPU,
		PriceUnit:     priceUnit,
	}
	return printer.PrintTableWithOptions(baseResults, opts)
}

func convertToInstanceTypeResults(findResults []find.FindResult) []aws.InstanceTypeResult {
	results := make([]aws.InstanceTypeResult, len(findResults))
	for i, r := range findResults {
		results[i] = r.InstanceTypeResult
	}
	return results
}

// instanceGeneration extracts the generation number from an instance type name.
// E.g., "c7i.large" → 7, "m6g.xlarge" → 6, "p4d.24xlarge" → 4.
func instanceGeneration(instanceType string) int {
	for i, ch := range instanceType {
		if ch >= '0' && ch <= '9' {
			gen := int(ch - '0')
			if i+1 < len(instanceType) && instanceType[i+1] >= '0' && instanceType[i+1] <= '9' {
				gen = gen*10 + int(instanceType[i+1]-'0')
			}
			return gen
		}
	}
	return 0
}

// looksLikePattern returns true if the query looks like an instance type pattern
// (glob or regex) rather than a natural language query.
func looksLikePattern(query string) bool {
	if strings.ContainsAny(query, "*?") {
		return true
	}
	if looksLikeRegex(query) {
		return true
	}
	// Single word that looks like an instance type (e.g. "m7i.large", "c6i",
	// "trn1.32xlarge"). The family prefix may be MULTIPLE letters before the
	// generation digit — AWS accelerator families are trn1/trn2 (Trainium),
	// inf1/inf2 (Inferentia), dl1 (Habana), vt1 (video). The old `^[a-z]\d`
	// (single leading letter) silently misrouted those to the natural-language
	// parser, which emitted a ".*" match-everything pattern and hung/returned the
	// whole catalog (#69-class bug). Match one-or-more leading letters + a
	// generation digit instead.
	//
	// EXCEPTION: a handful of real vocabulary terms also match this shape
	// (avx2, sve2, and any future "sveN"/"avxN"-style instruction set) — check
	// GPU/instruction-set/processor tables first so a recognized keyword is
	// never misrouted to the literal pattern matcher (which would 0-match it,
	// since no instance family is literally named "avx2" or "sve2").
	if !strings.Contains(query, " ") {
		if isKnownVocabularyTerm(query) {
			return false
		}
		if matched, _ := regexp.MatchString(`^[a-z]+\d`, query); matched {
			return true
		}
	}
	return false
}

// isKnownVocabularyTerm reports whether word is a recognized single-word
// GPU, processor, or instruction-set term (including aliases) — the same
// tables pkg/find's tokenizer itself checks. Used by looksLikePattern to
// avoid misrouting a real vocabulary word that happens to end in a digit
// (e.g. "avx2", "sve2") to the literal instance-type pattern matcher.
func isKnownVocabularyTerm(word string) bool {
	if _, ok := metadata.InstructionSetDatabase[word]; ok {
		return true
	}
	if _, ok := metadata.InstructionSetAliases[word]; ok {
		return true
	}
	if _, ok := metadata.GPUDatabase[word]; ok {
		return true
	}
	if _, ok := metadata.GPUAliases[word]; ok {
		return true
	}
	if _, ok := metadata.ProcessorDatabase[word]; ok {
		return true
	}
	return false
}

// runSearchWithPattern runs a pattern-based search (the same logic as the search command).
func runSearchWithPattern(pattern, service string) error {
	regexPattern := patternToRegex(pattern)
	matcher, err := regexp.Compile(regexPattern)
	if err != nil {
		return i18n.Te("truffle.search.error.invalid_pattern", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()

	awsClient, err := aws.NewClient(ctx)
	if err != nil {
		return i18n.Te("error.aws_client_init", err)
	}

	searchRegions := regions
	if len(searchRegions) == 0 {
		searchRegions, err = awsClient.GetEnabledRegions(ctx)
		if err != nil {
			return i18n.Te("truffle.search.error.get_regions_failed", err)
		}
		if len(searchRegions) > 10 && outputFormat == "table" {
			fmt.Fprintf(os.Stderr, "%s Searching across all %d enabled regions (this may take a while)\n",
				i18n.Emoji("warning"), len(searchRegions))
			fmt.Fprintf(os.Stderr, "   Tip: Use --regions to limit search (e.g., --regions us-east-1,us-west-2)\n\n")
		}
	}

	var spinner *progress.Spinner
	if !verbose && outputFormat == "table" {
		msg := fmt.Sprintf("Searching '%s' across %d %s...", pattern, len(searchRegions), pluralize(len(searchRegions), "region", "regions"))
		spinner = progress.NewSpinner(os.Stderr, msg)
		spinner.Start()
	}

	patternFilterOpts := aws.FilterOptions{
		IncludeAZs: !findSkipAZs,
		Verbose:    verbose,
	}
	var results []aws.InstanceTypeResult
	if service == "sagemaker" {
		results, err = awsClient.SearchSageMakerInstanceTypes(ctx, searchRegions, matcher, patternFilterOpts)
	} else {
		results, err = awsClient.SearchInstanceTypes(ctx, searchRegions, matcher, patternFilterOpts)
	}

	if spinner != nil {
		spinner.Stop()
	}

	if err != nil {
		return i18n.Te("truffle.search.error.search_failed", err)
	}

	sort.Slice(results, func(i, j int) bool {
		genI := instanceGeneration(results[i].InstanceType)
		genJ := instanceGeneration(results[j].InstanceType)
		if genI != genJ {
			return genI > genJ
		}
		if results[i].InstanceType != results[j].InstanceType {
			return results[i].InstanceType < results[j].InstanceType
		}
		return results[i].Region < results[j].Region
	})

	if len(results) == 0 {
		fmt.Println(i18n.T("truffle.search.no_results"))
		return nil
	}

	// Show the $/hr column when the user asks (--show-price), and always for
	// SageMaker ml.* types — those are priced under a distinct offer
	// (AmazonSageMaker) with a management premium, so the pattern path shows that
	// rate rather than a bare EC2 price.
	showPrice := findShowPrice || service == "sagemaker"
	if showPrice {
		for idx := range results {
			var price float64
			if service == "sagemaker" {
				price, _ = awsClient.SageMakerPrice(ctx, results[idx].InstanceType, results[idx].Region)
			} else {
				price, _ = awsClient.OnDemandPrice(ctx, results[idx].InstanceType, results[idx].Region)
			}
			results[idx].OnDemandPrice = price
		}
	}

	if findPickFirst {
		fmt.Println(results[0].InstanceType)
		return nil
	}

	printer := output.NewPrinter(!noColor)
	switch outputFormat {
	case "json":
		return printer.PrintJSON(results)
	case "yaml":
		return printer.PrintYAML(results)
	case "csv":
		return printer.PrintCSV(results)
	case "table":
		// findPriceUnit is validated in runFind before this path is reached.
		priceUnit, _ := validatePriceUnitFlag(findPriceUnit)
		opts := output.TableOptions{
			IncludeAZs:    !findSkipAZs,
			ShowPrice:     showPrice,
			ShowQuota:     findShowQuota && service == "sagemaker",
			ShowRealCores: findShowRealCores,
			ShowMemPerCPU: findShowMemPerCPU,
			PriceUnit:     priceUnit,
		}
		return printer.PrintTableWithOptions(results, opts)
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

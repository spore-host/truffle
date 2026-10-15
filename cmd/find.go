package cmd

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/catalog"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
	"github.com/spore-host/truffle/pkg/output"
	"github.com/spore-host/truffle/pkg/progress"
)

var (
	findSkipAZs       bool
	findLocalZones    bool // --local-zones flag: restrict results to Local/Wavelength AZs (#164)
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
	findShowGPURatios bool   // --show-gpu-ratios flag: adds vCPU/GPU and RAM/GPU columns (#51)
)

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Find instances by natural language, pattern, or specs",
	Long: `Find EC2 instance types using natural language, glob patterns, or regex.

Searches your configured region by default (spore config region, AWS_REGION /
AWS_DEFAULT_REGION, or the profile's region). Use --regions to choose regions,
or --all-regions to search every enabled region. With no region configured,
find searches every enabled region.

Auto-detects query type:
  - Patterns: m7i*, c[6-8]i.large, g5.* → pattern matching
  - Natural language: "graviton 8 cores 32gb" → spec-based search
  - Multiple explicit types as separate args compare side-by-side in one
    table: truffle find g5.2xlarge g5.4xlarge g5.12xlarge

Understands:
  - CPU vendors: intel, amd, graviton, nvidia
  - Processors: emerald rapids, sapphire rapids, ice lake, genoa, turin, milan
  - GPUs: h200, h100, a100, b200, b300, l40s, l4, a10g, t4, rtx, inferentia, trainium
  - Any accelerator: gpu, gpus, accelerator, accelerators (every GPU,
    Inferentia, Trainium, FPGA and media-accelerator family; "amd gpu" is the
    GPU hosts with an AMD CPU, e.g. g5 and g6, and "graviton gpu" is g5g)
  - Accelerator use case: training (H100, H200, A100, V100, B200, B300,
    Trainium, Trainium2) or inference (A10G, T4, L4, L40S, Inferentia,
    Inferentia2), as AWS positions each chip; name a GPU to get one outside
    its usual use case
    (hyphenated/suffixed spec strings also work, e.g. "RTX-PRO-6000",
    "A100-80GB", "H100!", "B200+" — Modal's documented GPU naming convention)
  - Instruction sets: avx2, avx-512, sve, sve2
  - NVIDIA MIG: mig (matches only GPUs supporting Multi-Instance GPU
    partitioning — A100, H100, H200, B200, RTX PRO 6000/4500; NOT
    L4/L40S/T4/A10G/V100/B300)
  - NVIDIA MPS: mps (matches ANY NVIDIA GPU — MPS is a CUDA runtime feature,
    not a hardware capability tied to instance type, unlike MIG above)
  - Specs: 8 cores, 8 physical cores, 32gb, 4 gpus
    (minimums: results are listed closest fit first, then cheapest — the
    request's exact size leads; add --exact to show only exact matches)
  - Sizes: tiny, small, medium, large, huge
  - Families and exact sizes: g5, m7i-flex, xlarge, 2xlarge, metal-24xl
    ("g5 xlarge" is g5.xlarge; "g5" alone is every g5 size)
  - Architecture: x86_64, arm64
  - Network: efa, 10gbps, 25gbps, 50gbps, 100gbps, 200gbps, 400gbps
  - Sort hints: cheap/cheapest, fast/fastest, newest/latest
  - A query with no recognised word is an error that names the unknown
    words, rather than a search of every instance type
  - Combining constraints: multiple constraint types (e.g. a GPU and "efa")
    combine with AND by default — "h100 efa" means H100 instances that also
    have EFA, not "either." Add the literal word "or" anywhere in the query
    to switch the whole query to OR (union) instead; "and" is also accepted
    explicitly. Within one constraint type, multiple values still always mean
    OR — "a100 h100" always means A100-or-H100, since no instance is both.

Examples:
  truffle find g5.2xlarge g5.4xlarge g5.12xlarge (multi-type comparison)
  truffle find "m7i*"                         (glob pattern)
  truffle find "m7i*" --all-regions           (every enabled region)
  truffle find "c[6-8]i.large"                (regex pattern)
  truffle find graviton                       (vendor search)
  truffle find "turin 32 cores 64gb" --exact  (exact spec match)
  truffle find "8 physical cores 32gb"        (physical core count)
  truffle find "cheap graviton 8 cores"       (sorted by price)
  truffle find nvidia                         (all NVIDIA GPU instances)
  truffle find "h100 efa"                     (GPU AND network, by default)
  truffle find "h100 or efa"                  (GPU OR network, explicit)
  truffle find avx-512                        (instruction-set search)
  truffle find "sve2 graviton"                (instruction set + vendor)
  truffle find mig                            (NVIDIA MIG-capable GPUs only)
  truffle find mps                            (any NVIDIA GPU — MPS runs on all of them)`,
	Args: cobra.ArbitraryArgs, // 0 args allowed when --app is used
	RunE: runFind,
}

func init() {
	rootCmd.AddCommand(findCmd)

	addAllRegionsFlag(findCmd, findRegionDefault)
	findCmd.Flags().BoolVar(&findSkipAZs, "skip-azs", false, "Skip availability zone lookup (faster)")
	findCmd.Flags().BoolVar(&findLocalZones, "local-zones", false, "Restrict results to Local Zones and Wavelength Zones (edge locations); mutually exclusive with --skip-azs")
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
	findCmd.Flags().BoolVar(&findShowGPURatios, "show-gpu-ratios", false, "Add vCPU/GPU and RAM/GPU columns for GPU instance types (decisive for CPU/IO-bound ML workloads)")
}

func runFind(cmd *cobra.Command, args []string) (err error) {
	// --local-zones filters on AZ data, so it can't coexist with --skip-azs
	// (which suppresses that data). Reject the contradiction with a clear error
	// rather than silently overriding either flag (#164).
	if findLocalZones && findSkipAZs {
		return fmt.Errorf("--local-zones cannot be combined with --skip-azs: filtering to Local/Wavelength zones needs availability-zone data")
	}

	service, err := validateServiceFlag(findService)
	if err != nil {
		return err
	}

	if _, err := validatePriceUnitFlag(findPriceUnit); err != nil {
		return err
	}

	// Multiple explicit instance types/patterns as separate args (e.g.
	// `truffle find g5.2xlarge g5.4xlarge g5.12xlarge`) — a first-class
	// side-by-side comparison, distinct from a multi-word natural-language
	// query (e.g. `truffle find 8 cores 32gb`), which must keep joining into
	// one query string below (#52). Only take this path when EVERY arg
	// individually looks like a pattern; if even one doesn't, args are a
	// single NL query whose words happen to be split across argv (the
	// pre-existing, unambiguous case) and get joined as before.
	if len(args) > 1 && findApp == "" && allLookLikePatterns(args) {
		return runSearchWithPatterns(args, service)
	}

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

	// Say which words were ignored, before searching, rather than only when
	// nothing matched (#229).
	if unknown := query.UnknownTerms(); len(unknown) > 0 {
		fmt.Fprintf(os.Stderr, "Note: ignored unknown %s: %s\n", pluralize(len(unknown), "term", "terms"), describeUnknownTerms(unknown))
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

	// Resolve regions: --regions > --all-regions > configured region > all (#188, #194)
	searchRegions, err := commandRegions(ctx, client, findRegionDefault)
	if err != nil {
		return err
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

	if err != nil {
		if spinner != nil {
			spinner.Stop()
		}
		return i18n.Te("truffle.search.error.search_failed", err)
	}

	// Classify Local/Wavelength zones so the AZ column can label them (#164).
	// Skipped implicitly when --skip-azs left results without AZ data.
	localZones := classifyLocalZoneAZs(ctx, client, results)

	// --local-zones: restrict results to Local/Wavelength AZs, dropping any type
	// with no edge-zone availability (#164). Applied before pricing, so dropped
	// rows are not priced, and before --pick-first so the top result reflects
	// the filter. Filtering keeps the order, so doing it before the sort below
	// gives the same rows in the same order as doing it after.
	searched := len(results)
	if findLocalZones {
		results = filterToLocalZones(results, localZones)
	}

	// Populate on-demand pricing. The table and the machine formats show a
	// price on every row, and closest-fit and price sorts use it, so every row
	// is priced — except under --pick-first, which prints one type and needs
	// prices only for the types that can still win once price breaks the tie.
	// Lookups are deduped and run concurrently, one Price List request per
	// type across regions (truffle#210). SageMaker ml.* types are priced under
	// a distinct offer (AmazonSageMaker) with a management premium.
	var want map[string]bool
	if findPickFirst {
		want = pickFirstPriceCandidates(results, criteria.FilterOptions, sortPref)
	}
	priced := priceResults(ctx, client, results, want, service)

	if spinner != nil {
		spinner.Stop()
	}

	// Order results closest-fit first (truffle#187), or by the qualitative
	// preference when one was given. The table renders this order as-is
	// (PreserveOrder), so table, JSON, YAML, CSV and --pick-first agree.
	sortFindResults(results, criteria.FilterOptions, sortPref)

	if len(results) == 0 {
		if ctx.Err() != nil {
			fmt.Fprintf(os.Stderr, "%s Request timed out after %s. Results may be incomplete.\n    Try increasing --timeout or narrowing --regions.\n", i18n.Symbol("warning"), findTimeout)
		}
		if searched > 0 {
			fmt.Fprintln(os.Stderr, "No instances match your query in any Local or Wavelength zone")
			return nil
		}
		fmt.Fprintln(os.Stderr, "No instances match your query")
		if query.HasNoMatchingFamily() {
			// e.g. "intel h100": the constraints exclude each other, so the
			// search matched nothing rather than everything (#229).
			fmt.Fprintln(os.Stderr, "  No instance family satisfies all of the query's constraints together; drop one, or add \"or\" to match any of them")
		}
		printSuggestions(query)
		return nil
	}

	// A pricing timeout is an error, reported after the results so the rows
	// that were priced are still shown (their missing prices read N/A).
	pricingErr := reportPricing(priced, findTimeout)

	// --pick-first: output just the instance type of the top result and exit
	if findPickFirst {
		if pricingErr != nil {
			return pricingErr
		}
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
	if err := printFindResults(enrichedResults, localZones); err != nil {
		return err
	}
	// Spec terms are minimums, so say so — and how to get an exact match —
	// next to the table. Human output only; machine formats stay clean.
	if outputFormat == "table" {
		if hint := minimumSpecHint(results, criteria.FilterOptions, sortPref); hint != "" {
			fmt.Fprintln(os.Stderr, hint)
		}
	}
	return pricingErr
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
	if len(query.Accelerators) > 0 {
		fmt.Fprintf(os.Stderr, "   Accelerator: %s\n", strings.Join(query.Accelerators, ", "))
	}
	if len(query.InstructionSets) > 0 {
		fmt.Fprintf(os.Stderr, "   Instruction set: %s\n", strings.Join(query.InstructionSets, ", "))
	}
	if query.RequireMIG {
		fmt.Fprintln(os.Stderr, "   NVIDIA MIG: required")
	}
	if len(query.Sizes) > 0 {
		fmt.Fprintf(os.Stderr, "   Size: %s\n", strings.Join(query.Sizes, ", "))
	}
	if len(query.Families) > 0 {
		fmt.Fprintf(os.Stderr, "   Family: %s\n", strings.Join(query.Families, ", "))
	}
	if len(query.InstanceSizes) > 0 {
		fmt.Fprintf(os.Stderr, "   Instance size: %s\n", strings.Join(query.InstanceSizes, ", "))
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

	if activeDimensionCount(query) > 1 {
		op := "AND (default)"
		if query.Operator == find.OperatorOr {
			op = "OR (explicit)"
		}
		fmt.Fprintf(os.Stderr, "   Combining constraints: %s\n", op)
	}

	// Show resolved families
	families := query.ResolveInstanceFamilies()
	if len(families) > 0 {
		fmt.Fprintf(os.Stderr, "   Instance families: %s\n", strings.Join(families, ", "))
	}

	fmt.Fprintln(os.Stderr)
}

// activeDimensionCount counts how many distinct query dimensions (vendor,
// processor, GPU, instruction set, EFA, MIG, network speed) the user
// specified — used to decide whether showing the AND/OR combination mode is
// meaningful (it isn't when only one dimension is active).
func activeDimensionCount(query *find.ParsedQuery) int {
	count := 0
	if len(query.Vendors) > 0 {
		count++
	}
	if len(query.Processors) > 0 {
		count++
	}
	if len(query.GPUs) > 0 {
		count++
	}
	if len(query.Accelerators) > 0 {
		count++
	}
	if len(query.InstructionSets) > 0 {
		count++
	}
	if len(query.Families) > 0 {
		count++
	}
	if query.RequireEFA {
		count++
	}
	if query.RequireMIG {
		count++
	}
	if query.MinNetworkGbps > 0 {
		count++
	}
	return count
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
		fmt.Fprintf(os.Stderr, "  - Unknown terms: %s\n", describeUnknownTerms(unknownTokens))
		fmt.Fprintln(os.Stderr, "  - Try: intel, amd, graviton, ice lake, milan, a100, v100, h100")
	}

	if len(query.Vendors) == 0 && len(query.Processors) == 0 && len(query.GPUs) == 0 && len(query.Accelerators) == 0 && len(query.InstructionSets) == 0 {
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

// describeUnknownTerms lists unknown query words with a suggestion for each
// that has one: `"gravitn" (did you mean "graviton"?), "zzqx"`.
func describeUnknownTerms(terms []string) string {
	suggestions := find.SuggestTerms(terms)
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = fmt.Sprintf("%q", t)
		if s, ok := suggestions[t]; ok {
			parts[i] += fmt.Sprintf(" (did you mean %q?)", s)
		}
	}
	return strings.Join(parts, ", ")
}

func printFindResults(results []find.FindResult, localZones map[string]bool) error {
	printer := output.NewPrinter(useColor())

	switch outputFormat {
	case "json":
		return printer.PrintJSON(convertToInstanceTypeResults(results))
	case "yaml":
		return printer.PrintYAML(convertToInstanceTypeResults(results))
	case "csv":
		return printer.PrintCSV(convertToInstanceTypeResults(results))
	case "table":
		return printFindTable(results, printer, localZones)
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

func printFindTable(results []find.FindResult, printer *output.Printer, localZones map[string]bool) error {
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
		ShowGPURatios: findShowGPURatios,
		PriceUnit:     priceUnit,
		LocalZoneAZs:  localZones,
		PreserveOrder: true, // already sorted closest-fit first by sortFindResults (#187)
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
	// A single word that fully resolves as known vocabulary — a GPU,
	// processor, or instruction-set term, including a hyphenated/suffixed
	// spec-string spelling like "RTX-PRO-6000", "A100-80GB", "H100!", "B200+"
	// (#130) — always takes the natural-language path, checked BEFORE the
	// wildcard/regex-indicator checks below. This has to come first: "+" is
	// one of the indicators looksLikeRegex treats as "this must be a regex",
	// so without this ordering "B200+"/"b200+" (a real, documented Modal
	// spec-string) matched looksLikeRegex and never reached the vocabulary
	// check that would have recognized it. No real GPU/processor/
	// instruction-set key contains '*' or '?' either, so checking vocabulary
	// first cannot misroute an actual glob/regex query.
	if !strings.Contains(query, " ") && find.IsRecognizedTerm(query) {
		return false
	}
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
	if !strings.Contains(query, " ") {
		if matched, _ := regexp.MatchString(`(?i)^[a-z]+\d`, query); matched {
			return true
		}
	}
	return false
}

// allLookLikePatterns reports whether every arg individually looks like an
// instance-type pattern (glob, regex, or exact type) rather than a
// natural-language term. Used to decide whether multiple args are a
// side-by-side multi-type comparison (`truffle find g5.2xlarge g5.4xlarge`,
// #52) versus a multi-word NL query whose words simply arrived as separate
// argv entries (`truffle find 8 cores 32gb`) — the latter must still be
// joined into one query string, since e.g. "8" and "cores" individually
// aren't patterns but "8 cores" together IS meaningful to ParseQuery.
func allLookLikePatterns(args []string) bool {
	for _, a := range args {
		if !looksLikePattern(a) {
			return false
		}
	}
	return true
}

// combinePatternsToRegex ORs multiple instance-type patterns into one regexp
// via alternation, so a multi-type comparison query still runs as a single
// SearchInstanceTypes call instead of N separate ones (#52). Each pattern is
// converted and anchored independently first (patternToRegex already wraps
// each in ^...$), then the anchors are stripped before joining so the
// alternation applies to the whole combined pattern, not to a suffix of it.
func combinePatternsToRegex(patterns []string) string {
	parts := make([]string, 0, len(patterns))
	for _, p := range patterns {
		re := patternToRegex(p)
		re = strings.TrimPrefix(re, "^")
		re = strings.TrimSuffix(re, "$")
		parts = append(parts, "(?:"+re+")")
	}
	return "^(?:" + strings.Join(parts, "|") + ")$"
}

// runSearchWithPatterns is the multi-type sibling of runSearchWithPattern
// (#52): it combines every pattern into one alternated regex and runs a
// single search, so `truffle find g5.2xlarge g5.4xlarge g5.12xlarge` renders
// all three as rows in one comparison table instead of requiring one
// invocation per type. Shares the same downstream rendering path as the
// single-pattern case — only pattern compilation and the display label for
// the search-progress spinner differ.
func runSearchWithPatterns(patterns []string, service string) error {
	return runSearchWithPatternDisplay(combinePatternsToRegex(patterns), strings.Join(patterns, ", "), service)
}

// runSearchWithPattern runs a pattern-based search (the same logic as the search command).
func runSearchWithPattern(pattern, service string) error {
	return runSearchWithPatternDisplay(patternToRegex(pattern), pattern, service)
}

// runSearchWithPatternDisplay is the shared implementation behind
// runSearchWithPattern and runSearchWithPatterns: regexPattern is the
// already-built, already-anchored regex to search with; display is what the
// progress spinner shows (a single pattern, or "type1, type2, type3" for a
// multi-type comparison).
func runSearchWithPatternDisplay(regexPattern, display, service string) error {
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

	searchRegions, err := commandRegions(ctx, awsClient, findRegionDefault)
	if err != nil {
		return err
	}

	var spinner *progress.Spinner
	if !verbose && outputFormat == "table" {
		msg := fmt.Sprintf("Searching '%s' across %d %s...", display, len(searchRegions), pluralize(len(searchRegions), "region", "regions"))
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

	// Classify Local/Wavelength zones once — used to label the AZ column and,
	// with --local-zones, to filter results down to edge zones (#164).
	var localZones map[string]bool
	if !findSkipAZs || findLocalZones {
		localZones = classifyLocalZoneAZs(ctx, awsClient, results)
	}
	if findLocalZones {
		results = filterToLocalZones(results, localZones)
		if len(results) == 0 {
			fmt.Println(i18n.T("truffle.search.no_results"))
			return nil
		}
	}

	// --pick-first prints only a type name, and this path orders by generation
	// and name, not price, so it needs no prices at all.
	if findPickFirst {
		fmt.Println(results[0].InstanceType)
		return nil
	}

	// Show the $/hr column when the user asks (--show-price), and always for
	// SageMaker ml.* types — those are priced under a distinct offer
	// (AmazonSageMaker) with a management premium, so the pattern path shows that
	// rate rather than a bare EC2 price. Lookups are deduped and concurrent (#210).
	showPrice := findShowPrice || service == "sagemaker"
	var pricingErr error
	if showPrice {
		pricingErr = reportPricing(priceResults(ctx, awsClient, results, nil, service), findTimeout)
	}

	printer := output.NewPrinter(useColor())
	var printErr error
	switch outputFormat {
	case "json":
		printErr = printer.PrintJSON(results)
	case "yaml":
		printErr = printer.PrintYAML(results)
	case "csv":
		printErr = printer.PrintCSV(results)
	case "table":
		// findPriceUnit is validated in runFind before this path is reached.
		priceUnit, _ := validatePriceUnitFlag(findPriceUnit)
		opts := output.TableOptions{
			IncludeAZs:    !findSkipAZs,
			ShowPrice:     showPrice,
			ShowQuota:     findShowQuota && service == "sagemaker",
			ShowRealCores: findShowRealCores,
			ShowMemPerCPU: findShowMemPerCPU,
			ShowGPURatios: findShowGPURatios,
			PriceUnit:     priceUnit,
			LocalZoneAZs:  localZones,
		}
		printErr = printer.PrintTableWithOptions(results, opts)
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
	if printErr != nil {
		return printErr
	}
	return pricingErr
}

// fitDistance scores how far an instance type overshoots the requested
// minimums (truffle#187). Each specified dimension (vCPU, memory, physical
// cores) contributes |log2(actual/requested)|, so an exact match scores 0 and
// doubling any one dimension costs the same 1.0 — 16 vCPU/128 GiB and
// 32 vCPU/64 GiB are equally far from a "16 cores 64gb" request, and
// 192 vCPU/1536 GiB is far away. Unspecified dimensions contribute nothing,
// so with no size terms every type scores 0.
func fitDistance(r aws.InstanceTypeResult, f aws.FilterOptions) float64 {
	d := 0.0
	add := func(actual, want float64) {
		if want > 0 && actual > 0 {
			d += math.Abs(math.Log2(actual / want))
		}
	}
	add(float64(r.VCPUs), float64(f.MinVCPUs))
	add(float64(r.MemoryMiB)/1024.0, f.MinMemory)
	add(float64(r.PhysicalCores), float64(f.MinPhysicalCores))
	return d
}

// hasSizeSpec reports whether the query constrained vCPU, memory, or cores.
func hasSizeSpec(f aws.FilterOptions) bool {
	return f.MinVCPUs > 0 || f.MinMemory > 0 || f.MinPhysicalCores > 0
}

// sortFindResults orders find results in place. Rows for one instance type
// stay contiguous (types are ranked as a unit, regardless of region), and
// within a type the rows go cheapest region first (most expensive first for
// "expensive"); unknown prices sort last.
//
// Default ranking (truffle#187): when the query has size terms, closest fit
// first (see fitDistance), then cheapest; with no size terms, newest
// generation first, then cheapest. Qualitative keywords (cheap, expensive,
// fast, newest) override the primary key.
func sortFindResults(results []aws.InstanceTypeResult, f aws.FilterOptions, pref find.SortPreference) {
	type typeKey struct {
		fit                float64
		gen                int
		vcpus              int32
		minPrice, maxPrice float64 // 0 = unknown in every region
	}
	keys := make(map[string]*typeKey)
	for _, r := range results {
		k, ok := keys[r.InstanceType]
		if !ok {
			k = &typeKey{
				fit:   fitDistance(r, f),
				gen:   instanceGeneration(r.InstanceType),
				vcpus: r.VCPUs,
			}
			keys[r.InstanceType] = k
		}
		if p := r.OnDemandPrice; p > 0 {
			if k.minPrice == 0 || p < k.minPrice {
				k.minPrice = p
			}
			if p > k.maxPrice {
				k.maxPrice = p
			}
		}
	}

	// cmpPrice orders known prices ascending (or descending) with unknown (0)
	// always last; returns 0 when equal.
	cmpPrice := func(a, b float64, desc bool) int {
		switch {
		case a == b:
			return 0
		case a == 0:
			return 1
		case b == 0:
			return -1
		case (a < b) != desc:
			return -1
		default:
			return 1
		}
	}
	const eps = 1e-9
	cmpFit := func(a, b *typeKey) int {
		switch {
		case a.fit < b.fit-eps:
			return -1
		case a.fit > b.fit+eps:
			return 1
		}
		return 0
	}
	cmpGen := func(a, b *typeKey) int {
		switch {
		case a.gen > b.gen:
			return -1
		case a.gen < b.gen:
			return 1
		}
		return 0
	}
	sizeSpec := hasSizeSpec(f)

	sort.SliceStable(results, func(i, j int) bool {
		ri, rj := results[i], results[j]
		if ri.InstanceType != rj.InstanceType {
			a, b := keys[ri.InstanceType], keys[rj.InstanceType]
			var order []int
			switch pref {
			case find.SortCheapest:
				order = []int{cmpPrice(a.minPrice, b.minPrice, false), cmpFit(a, b), cmpGen(a, b)}
			case find.SortExpensive:
				order = []int{cmpPrice(a.maxPrice, b.maxPrice, true), cmpFit(a, b), cmpGen(a, b)}
			case find.SortPerformant:
				v := 0
				if a.vcpus != b.vcpus {
					v = 1
					if a.vcpus > b.vcpus {
						v = -1
					}
				}
				order = []int{v, cmpPrice(a.minPrice, b.minPrice, false), cmpGen(a, b)}
			case find.SortNewest:
				order = []int{cmpGen(a, b), cmpFit(a, b), cmpPrice(a.minPrice, b.minPrice, false)}
			default:
				if sizeSpec {
					order = []int{cmpFit(a, b), cmpPrice(a.minPrice, b.minPrice, false), cmpGen(a, b)}
				} else {
					order = []int{cmpGen(a, b), cmpPrice(a.minPrice, b.minPrice, false)}
				}
			}
			for _, c := range order {
				if c != 0 {
					return c < 0
				}
			}
			return ri.InstanceType < rj.InstanceType
		}
		if c := cmpPrice(ri.OnDemandPrice, rj.OnDemandPrice, pref == find.SortExpensive); c != 0 {
			return c < 0
		}
		return ri.Region < rj.Region
	})
}

// minimumSpecHint is the one-line note shown under the find table explaining
// that size terms are minimums (truffle#187), e.g. "Showing 28 types with
// ≥16 vCPU and ≥64 GiB memory, closest fit first; use --exact for an exact
// match" (the order clause only for the default sort). Empty when the query had no size terms or used --exact.
func minimumSpecHint(results []aws.InstanceTypeResult, f aws.FilterOptions, pref find.SortPreference) string {
	if !hasSizeSpec(f) || f.ExactVCPUs || f.ExactMemory || f.ExactCores {
		return ""
	}
	types := make(map[string]bool)
	for _, r := range results {
		types[r.InstanceType] = true
	}
	var terms []string
	if f.MinVCPUs > 0 {
		terms = append(terms, fmt.Sprintf("≥%d vCPU", f.MinVCPUs))
	}
	if f.MinPhysicalCores > 0 {
		terms = append(terms, fmt.Sprintf("≥%d physical cores", f.MinPhysicalCores))
	}
	if f.MinMemory > 0 {
		terms = append(terms, fmt.Sprintf("≥%s GiB memory", strconv.FormatFloat(f.MinMemory, 'f', -1, 64)))
	}
	order := ""
	if pref == find.SortDefault {
		order = ", closest fit first"
	}
	return fmt.Sprintf("Showing %d %s with %s%s; use --exact for an exact match",
		len(types), pluralize(len(types), "type", "types"), strings.Join(terms, " and "), order)
}

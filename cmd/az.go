package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/output"
	"gopkg.in/yaml.v3"
)

var (
	azFilter       []string
	minAZCount     int
	showRegionOnly bool
	azLocalZones   bool // --local-zones flag: restrict results to Local/Wavelength AZs (#164)
)

// errAZAllRegionsConflict: --az already chooses the regions (those its AZs
// belong to), so --all-regions contradicts it.
var errAZAllRegionsConflict = errors.New("--all-regions cannot be combined with --az: --az already selects the regions its zones belong to")

var azCmd = &cobra.Command{
	Use:   "az [instance-type-pattern]",
	Short: "Search by availability zone (AZ-first view)",
	Long: `Search for instance types with an availability zone-first perspective.
This command prioritizes showing which specific AZs support each instance type,
making it ideal for multi-AZ deployments and capacity planning.

Searches every enabled region by default, since the point is to compare
regions; use --regions (or --az, which implies its zones' regions) to narrow.

Examples:
  # Find which AZs have m7i.large
  truffle az m7i.large

  # Search in specific AZs only
  truffle az m7i.large --az us-east-1a,us-east-1b

  # Find instances available in at least 3 AZs per region
  truffle az "m8g.*" --min-az-count 3

  # Just the regions that offer m7i.large in at least 3 AZs
  truffle az m7i.large --min-az-count 3 --regions-only

  # Show AZ availability summary
  truffle az "c7i.xlarge" --output json`,
	Args: cobra.ExactArgs(1),
	RunE: runAZSearch,
}

func init() {
	rootCmd.AddCommand(azCmd)

	addAllRegionsFlag(azCmd, azRegionDefault)
	azCmd.Flags().StringSliceVar(&azFilter, "az", []string{}, "Filter by specific availability zones (e.g., us-east-1a,us-west-2b)")
	azCmd.Flags().IntVar(&minAZCount, "min-az-count", 0, "Minimum number of AZs required per region")
	azCmd.Flags().BoolVar(&showRegionOnly, "regions-only", false, "Show only the regions that meet the AZ requirement (--min-az-count): one row per region with its AZ count, AZs and matching types, instead of one per instance type")
	azCmd.Flags().BoolVar(&azLocalZones, "local-zones", false, "Restrict results to Local Zones and Wavelength Zones (edge locations)")
	azCmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")

	// Register completion for instance type argument
	azCmd.ValidArgsFunction = completeInstanceType
}

func runAZSearch(cmd *cobra.Command, args []string) error {
	pattern := args[0]

	// Convert wildcard pattern to regex
	regexPattern := wildcardToRegex(pattern)
	matcher, err := regexp.Compile(regexPattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "%s Searching for instance types matching: %s (AZ-focused)\n", i18n.Emoji("magnifying_glass"), pattern)
		if len(azFilter) > 0 {
			fmt.Fprintf(os.Stderr, "%s Filtering AZs: %s\n", i18n.Emoji("pushpin"), strings.Join(azFilter, ", "))
		}
		if minAZCount > 0 {
			fmt.Fprintf(os.Stderr, "%s Minimum AZs per region: %d\n", i18n.Emoji("chart"), minAZCount)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Initialize AWS client
	awsClient, err := aws.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize AWS client: %w", err)
	}

	// Resolve regions. --az names the regions to search (its AZs' regions) and
	// takes precedence over --regions, as before; neither combines with
	// --all-regions. az defaults to every enabled region: it answers "which
	// regions have this type in enough AZs?", a cross-region question (#194).
	explicit := extractRegionsFromAZs(azFilter)
	if len(explicit) > 0 && allRegions {
		return errAZAllRegionsConflict
	}
	if len(explicit) == 0 {
		explicit = regions
	}
	searchRegions, err := commandRegionsFrom(ctx, awsClient, azRegionDefault, explicit)
	if err != nil {
		return err
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "%s Searching across %d regions...\n", i18n.Emoji("magnifying_glass_tilted"), len(searchRegions))
	}

	// Search for instance types (always include AZs for this command)
	results, err := awsClient.SearchInstanceTypes(ctx, searchRegions, matcher, aws.FilterOptions{
		IncludeAZs:     true, // Always get AZs for az command
		Architecture:   architecture,
		MinVCPUs:       minVCPUs,
		MinMemory:      minMemory,
		InstanceFamily: instanceFamily,
		Verbose:        verbose,
	})
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	// Filter by specific AZs if requested
	if len(azFilter) > 0 {
		results = filterByAZs(results, azFilter)
	}

	// Filter by minimum AZ count
	if minAZCount > 0 {
		results = filterByMinAZCount(results, minAZCount)
	}

	// Classify Local/Wavelength zones once — used to label the AZ column and,
	// with --local-zones, to filter results down to edge zones (#164).
	localZones := classifyLocalZoneAZs(ctx, awsClient, results)

	// --local-zones: restrict results to Local/Wavelength AZs, dropping any type
	// with no edge-zone availability (#164).
	if azLocalZones {
		results = filterToLocalZones(results, localZones)
	}

	// Sort by AZ count (most AZs first), then by instance type
	sort.Slice(results, func(i, j int) bool {
		countI := len(results[i].AvailableAZs)
		countJ := len(results[j].AvailableAZs)
		if countI != countJ {
			return countI > countJ
		}
		if results[i].InstanceType != results[j].InstanceType {
			return results[i].InstanceType < results[j].InstanceType
		}
		return results[i].Region < results[j].Region
	})

	if len(results) == 0 {
		fmt.Println("No matching instance types found with specified AZ criteria.")
		return nil
	}

	return printAZResults(results, localZones)
}

// printAZResults writes az's output in the selected --output format: the
// per-type, per-region table by default, or with --regions-only one row per
// qualifying region (#199).
func printAZResults(results []aws.InstanceTypeResult, localZones map[string]bool) error {
	printer := output.NewPrinter(useColor())

	if showRegionOnly {
		return printAZRegions(summarizeAZRegions(results))
	}

	// Print summary (table output only — keeps stdout clean for json/csv/yaml)
	if outputFormat == "table" {
		printAZSummary(results)
	}

	switch outputFormat {
	case "json":
		return printer.PrintJSON(results)
	case "yaml":
		return printer.PrintYAML(results)
	case "csv":
		return printer.PrintCSV(results)
	case "table":
		// Always show AZs; label any Local/Wavelength zones by their real
		// ZoneType (#164).
		return printer.PrintTableWithOptions(results, output.TableOptions{
			IncludeAZs:   true,
			LocalZoneAZs: localZones,
		})
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

// azRegionSummary is one row of `az --regions-only`: a region where at least
// one matching instance type passed every filter (--min-az-count, --az,
// --local-zones), with the AZs those types are offered in.
type azRegionSummary struct {
	Region string `json:"region" yaml:"region"`
	// AZCount is len(AvailabilityZones).
	AZCount int `json:"az_count" yaml:"az_count"`
	// AvailabilityZones is the union, across the region's qualifying instance
	// types, of the AZs each is offered in. Each type on its own meets
	// --min-az-count, so AZCount does too.
	AvailabilityZones []string `json:"availability_zones" yaml:"availability_zones"`
	// InstanceTypes are the matching types that qualified in this region.
	InstanceTypes []string `json:"instance_types" yaml:"instance_types"`
}

// summarizeAZRegions collapses az's per-type results (already filtered) into
// one entry per region, most AZs first, then by region name.
func summarizeAZRegions(results []aws.InstanceTypeResult) []azRegionSummary {
	azs := make(map[string]map[string]bool)
	types := make(map[string]map[string]bool)
	for _, r := range results {
		if azs[r.Region] == nil {
			azs[r.Region] = make(map[string]bool)
			types[r.Region] = make(map[string]bool)
		}
		for _, az := range r.AvailableAZs {
			azs[r.Region][az] = true
		}
		types[r.Region][r.InstanceType] = true
	}

	summaries := make([]azRegionSummary, 0, len(azs))
	for region := range azs {
		s := azRegionSummary{
			Region:            region,
			AvailabilityZones: sortedKeys(azs[region]),
			InstanceTypes:     sortedKeys(types[region]),
		}
		s.AZCount = len(s.AvailabilityZones)
		summaries = append(summaries, s)
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].AZCount != summaries[j].AZCount {
			return summaries[i].AZCount > summaries[j].AZCount
		}
		return summaries[i].Region < summaries[j].Region
	})
	return summaries
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// printAZRegions writes the --regions-only view to stdout.
func printAZRegions(summaries []azRegionSummary) error {
	switch outputFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summaries)
	case "yaml":
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(summaries)
	case "csv":
		w := csv.NewWriter(os.Stdout)
		_ = w.Write([]string{"region", "az_count", "availability_zones", "instance_types"})
		for _, s := range summaries {
			_ = w.Write([]string{s.Region, strconv.Itoa(s.AZCount), strings.Join(s.AvailabilityZones, ";"), strings.Join(s.InstanceTypes, ";")})
		}
		w.Flush()
		return w.Error()
	case "table":
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "REGION\tAZS\tAVAILABILITY ZONES\tINSTANCE TYPES")
		for _, s := range summaries {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", s.Region, s.AZCount, strings.Join(s.AvailabilityZones, ", "), strings.Join(s.InstanceTypes, ", "))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		noun := "regions"
		if len(summaries) == 1 {
			noun = "region"
		}
		_, err := fmt.Printf("\n%d %s\n", len(summaries), noun)
		return err
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

func extractRegionsFromAZs(azs []string) []string {
	regionMap := make(map[string]bool)
	for _, az := range azs {
		// Extract region from AZ (e.g., us-east-1a -> us-east-1)
		if len(az) > 1 {
			region := az[:len(az)-1] // Remove last character
			regionMap[region] = true
		}
	}

	regions := make([]string, 0, len(regionMap))
	for region := range regionMap {
		regions = append(regions, region)
	}
	return regions
}

func filterByAZs(results []aws.InstanceTypeResult, azFilter []string) []aws.InstanceTypeResult {
	azSet := make(map[string]bool)
	for _, az := range azFilter {
		azSet[az] = true
	}

	filtered := make([]aws.InstanceTypeResult, 0)
	for _, result := range results {
		// Check if any of the result's AZs match the filter
		hasMatch := false
		matchedAZs := make([]string, 0)
		for _, az := range result.AvailableAZs {
			if azSet[az] {
				hasMatch = true
				matchedAZs = append(matchedAZs, az)
			}
		}
		if hasMatch {
			result.AvailableAZs = matchedAZs // Only show matching AZs
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func filterByMinAZCount(results []aws.InstanceTypeResult, minCount int) []aws.InstanceTypeResult {
	filtered := make([]aws.InstanceTypeResult, 0)
	for _, result := range results {
		if len(result.AvailableAZs) >= minCount {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func printAZSummary(results []aws.InstanceTypeResult) {
	// Count unique instance types
	instanceTypes := make(map[string]bool)
	totalAZs := 0
	maxAZs := 0
	minAZs := 999

	for _, result := range results {
		instanceTypes[result.InstanceType] = true
		azCount := len(result.AvailableAZs)
		totalAZs += azCount
		if azCount > maxAZs {
			maxAZs = azCount
		}
		if azCount < minAZs && azCount > 0 {
			minAZs = azCount
		}
	}

	if minAZs == 999 {
		minAZs = 0
	}

	avgAZs := 0.0
	if len(results) > 0 {
		avgAZs = float64(totalAZs) / float64(len(results))
	}

	fmt.Printf("\n%s AZ Availability Summary:\n", i18n.Emoji("chart"))
	fmt.Printf("   Instance Types: %d\n", len(instanceTypes))
	fmt.Printf("   Region Results: %d\n", len(results))
	fmt.Printf("   AZ Range: %d-%d AZs per region\n", minAZs, maxAZs)
	fmt.Printf("   Average AZs: %.1f per region\n\n", avgAZs)
}

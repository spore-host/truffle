package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/output"
)

var (
	crInstanceTypes []string
	crOnlyAvailable bool
	crOnlyActive    bool
	crMinCapacity   int
	crGPUOnly       bool
	crShowBlocks    bool
	crShowODCR      bool
)

var capacityCmd = &cobra.Command{
	Use:  "capacity",
	RunE: runCapacity,
	// Short and Long will be set after i18n initialization
}

func init() {
	rootCmd.AddCommand(capacityCmd)

	capacityCmd.Flags().StringSliceVar(&crInstanceTypes, "instance-types", []string{}, "Filter by instance types (comma-separated)")
	capacityCmd.Flags().BoolVar(&crOnlyAvailable, "available-only", false, "Only show reservations with available capacity")
	capacityCmd.Flags().BoolVar(&crOnlyActive, "active-only", true, "Only show active reservations (default: true)")
	capacityCmd.Flags().IntVar(&crMinCapacity, "min-capacity", 0, "Minimum available capacity")
	capacityCmd.Flags().BoolVar(&crGPUOnly, "gpu-only", false, "Only show GPU/ML instance reservations")
	capacityCmd.Flags().BoolVar(&crShowBlocks, "blocks", false, "Show Capacity Blocks for ML (training workloads)")
	capacityCmd.Flags().BoolVar(&crShowODCR, "odcr", true, "Show On-Demand Capacity Reservations (default)")
	capacityCmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")
}

func runCapacity(cmd *cobra.Command, args []string) error {
	if verbose {
		fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("magnifying_glass"), i18n.T("truffle.capacity.searching"))
		if crGPUOnly {
			fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("video_game"), i18n.T("truffle.capacity.gpu_only"))
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
			fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("globe"), i18n.T("truffle.capacity.fetching_regions"))
		}
		searchRegions, err = awsClient.GetEnabledRegions(ctx)
		if err != nil {
			return i18n.Te("truffle.capacity.error.get_regions_failed", err)
		}
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("magnifying_glass_tilted"), i18n.Tf("truffle.capacity.searching_across", map[string]interface{}{
			"Count": len(searchRegions),
		}))
	}

	// Get capacity reservations
	results, err := awsClient.GetCapacityReservations(ctx, searchRegions, aws.CapacityReservationOptions{
		InstanceTypes: crInstanceTypes,
		OnlyAvailable: crOnlyAvailable,
		OnlyActive:    crOnlyActive,
		MinCapacity:   int32(crMinCapacity),
		Verbose:       verbose,
	})
	if err != nil {
		return i18n.Te("truffle.capacity.error.get_failed", err)
	}

	// Filter GPU instances if requested
	if crGPUOnly {
		results = filterGPUInstances(results)
	}

	if len(results) == 0 {
		fmt.Println(i18n.T("truffle.capacity.no_results"))
		return nil
	}

	// Sort by available capacity (most available first), then by instance type
	sort.Slice(results, func(i, j int) bool {
		if results[i].AvailableCapacity != results[j].AvailableCapacity {
			return results[i].AvailableCapacity > results[j].AvailableCapacity
		}
		if results[i].InstanceType != results[j].InstanceType {
			return results[i].InstanceType < results[j].InstanceType
		}
		return results[i].Region < results[j].Region
	})

	// Print summary (table output only — keeps stdout clean for json/csv/yaml)
	if outputFormat == "table" {
		printCapacitySummary(results)
	}

	// Output results
	printer := output.NewPrinter(!noColor)
	switch outputFormat {
	case "json":
		return printer.PrintCapacityJSON(results)
	case "yaml":
		return printer.PrintCapacityYAML(results)
	case "csv":
		return printer.PrintCapacityCSV(results)
	case "table":
		return printer.PrintCapacityTable(results)
	default:
		return i18n.Te("truffle.capacity.error.unsupported_format", nil, map[string]interface{}{
			"Format": outputFormat,
		})
	}
}

// gpuFamilyPrefixes are the GPU/ML instance-family generations. We match by
// prefix (not exact family) so suffixed variants are caught — e.g. "p4d"/"p4de"
// (A100), "p5e"/"p5en" (H100), "g4dn"/"g4ad" (T4), "trn1n" (Trainium). Matching
// the exact family would silently miss these (see #8).
var gpuFamilyPrefixes = []string{
	"p3",   // NVIDIA V100
	"p4",   // NVIDIA A100 (p4d, p4de)
	"p5",   // NVIDIA H100 (p5, p5e, p5en)
	"g4",   // NVIDIA T4 / AMD (g4dn, g4ad)
	"g5",   // NVIDIA A10G (g5, g5g)
	"g6",   // NVIDIA L4/L40S (g6, g6e, gr6)
	"inf1", // AWS Inferentia
	"inf2", // AWS Inferentia2
	"trn1", // AWS Trainium (trn1, trn1n)
	"trn2", // AWS Trainium2
	"vt1",  // Video transcoding
}

// isGPUFamily reports whether an instance family belongs to a GPU/ML generation.
func isGPUFamily(family string) bool {
	for _, p := range gpuFamilyPrefixes {
		if strings.HasPrefix(family, p) {
			return true
		}
	}
	return false
}

func filterGPUInstances(results []aws.CapacityReservationResult) []aws.CapacityReservationResult {
	filtered := make([]aws.CapacityReservationResult, 0)
	for _, r := range results {
		if isGPUFamily(extractFamily(r.InstanceType)) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func extractFamily(instanceType string) string {
	// Extract family from instance type (e.g., "p5" from "p5.48xlarge")
	parts := strings.Split(instanceType, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return instanceType
}

func printCapacitySummary(results []aws.CapacityReservationResult) {
	instanceTypes := make(map[string]bool)
	regions := make(map[string]bool)
	azs := make(map[string]bool)

	var totalCapacity, availableCapacity, usedCapacity int32
	activeCount := 0

	for _, r := range results {
		instanceTypes[r.InstanceType] = true
		regions[r.Region] = true
		azs[r.AvailabilityZone] = true

		totalCapacity += r.TotalCapacity
		availableCapacity += r.AvailableCapacity
		usedCapacity += r.UsedCapacity

		if r.State == "active" {
			activeCount++
		}
	}

	utilizationPercent := 0.0
	if totalCapacity > 0 {
		utilizationPercent = float64(usedCapacity) / float64(totalCapacity) * 100
	}

	fmt.Printf("\n%s %s\n", i18n.Emoji("chart"), i18n.T("truffle.capacity.summary.title"))
	fmt.Printf("   %s: %d\n", i18n.T("truffle.capacity.summary.total_reservations"), len(results))
	fmt.Printf("   %s: %d\n", i18n.T("truffle.capacity.summary.active_reservations"), activeCount)
	fmt.Printf("   %s: %d\n", i18n.T("truffle.capacity.summary.instance_types"), len(instanceTypes))
	fmt.Printf("   %s: %d\n", i18n.T("truffle.capacity.summary.regions"), len(regions))
	fmt.Printf("   %s: %d\n", i18n.T("truffle.capacity.summary.availability_zones"), len(azs))
	fmt.Printf("   %s: %d %s\n", i18n.T("truffle.capacity.summary.total_capacity"), totalCapacity, i18n.T("truffle.capacity.summary.instances"))
	fmt.Printf("   %s: %d %s (%.1f%% %s)\n", i18n.T("truffle.capacity.summary.available_capacity"), availableCapacity, i18n.T("truffle.capacity.summary.instances"), 100-utilizationPercent, i18n.T("truffle.capacity.summary.free"))
	fmt.Printf("   %s: %d %s (%.1f%% %s)\n", i18n.T("truffle.capacity.summary.used_capacity"), usedCapacity, i18n.T("truffle.capacity.summary.instances"), utilizationPercent, i18n.T("truffle.capacity.summary.utilized"))
	fmt.Println()
}

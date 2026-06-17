package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/output"
)

var (
	cboInstanceType  string
	cboInstanceCount int
	cboDurationHours int
	cboStartAfter    string
	cboStartBefore   string
)

var capacityBlocksCmd = &cobra.Command{
	Use:   "capacity-blocks",
	Short: "Discover purchasable EC2 Capacity Block for ML offerings",
	Long: `Discover purchasable EC2 Capacity Block for ML offerings (read-only).

This queries DescribeCapacityBlockOfferings — "what can I reserve?" — and shows
each offering's id, instance type/count, AZ, start/end, duration, and up-front
price. The offering id is what 'spawn capacity-block purchase' reserves.

For Capacity Blocks you ALREADY own, use 'truffle capacity --blocks' instead.

Examples:
  truffle capacity-blocks --instance-type p5.48xlarge --count 1 --duration-hours 24
  truffle capacity-blocks --instance-type p5.48xlarge --count 2 --duration-hours 48 \
    --region us-east-1 --output json`,
	RunE: runCapacityBlocksOfferings,
}

func init() {
	rootCmd.AddCommand(capacityBlocksCmd)

	capacityBlocksCmd.Flags().StringVar(&cboInstanceType, "instance-type", "", "Instance type to find offerings for (required, e.g. p5.48xlarge)")
	capacityBlocksCmd.Flags().IntVar(&cboInstanceCount, "count", 1, "Number of instances in the block")
	capacityBlocksCmd.Flags().IntVar(&cboDurationHours, "duration-hours", 0, "Capacity Block duration in hours (required, e.g. 24)")
	capacityBlocksCmd.Flags().StringVar(&cboStartAfter, "start-after", "", "Only offerings starting after this time (RFC3339, e.g. 2026-07-01T00:00:00Z)")
	capacityBlocksCmd.Flags().StringVar(&cboStartBefore, "start-before", "", "Only offerings ending before this time (RFC3339)")
	capacityBlocksCmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")
}

func runCapacityBlocksOfferings(cmd *cobra.Command, args []string) error {
	if cboInstanceType == "" {
		return fmt.Errorf("--instance-type is required (e.g. --instance-type p5.48xlarge)")
	}
	if cboDurationHours <= 0 {
		return fmt.Errorf("--duration-hours is required and must be > 0 (e.g. --duration-hours 24)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	awsClient, err := aws.NewClient(ctx)
	if err != nil {
		return i18n.Te("error.aws_client_init", err)
	}

	searchRegions := regions
	if len(searchRegions) == 0 {
		searchRegions, err = awsClient.GetEnabledRegions(ctx)
		if err != nil {
			return i18n.Te("truffle.capacity.error.get_regions_failed", err)
		}
	}

	results, err := awsClient.GetCapacityBlockOfferings(ctx, searchRegions, aws.CapacityBlockOfferingOptions{
		InstanceType:          cboInstanceType,
		InstanceCount:         int32(cboInstanceCount),
		CapacityDurationHours: int32(cboDurationHours),
		StartAfter:            cboStartAfter,
		StartBefore:           cboStartBefore,
		Verbose:               verbose,
	})
	if err != nil {
		return i18n.Te("truffle.capacity.error.get_failed", err)
	}

	if len(results) == 0 {
		fmt.Println(i18n.T("truffle.capacity.no_results"))
		return nil
	}

	// Sort cheapest-first by start date then offering id (stable, predictable).
	sort.Slice(results, func(i, j int) bool {
		if results[i].StartDate != results[j].StartDate {
			return results[i].StartDate < results[j].StartDate
		}
		return results[i].OfferingID < results[j].OfferingID
	})

	if outputFormat == "table" {
		fmt.Fprintf(os.Stderr, "Found %d Capacity Block offering(s) for %s ×%d (%dh). Purchase with: spawn capacity-block purchase <offering-id>\n\n",
			len(results), cboInstanceType, cboInstanceCount, cboDurationHours)
	}

	printer := output.NewPrinter(!noColor)
	switch outputFormat {
	case "json":
		return printer.PrintBlockOfferingsJSON(results)
	case "yaml":
		return printer.PrintBlockOfferingsYAML(results)
	case "csv":
		return printer.PrintBlockOfferingsCSV(results)
	case "table":
		return printer.PrintBlockOfferingsTable(results)
	default:
		return i18n.Te("truffle.capacity.error.unsupported_format", nil, map[string]interface{}{
			"Format": outputFormat,
		})
	}
}

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"gopkg.in/yaml.v3"
)

var availableCmd = &cobra.Command{
	Use:   "available <instance-type>",
	Short: "Report how obtainable an instance type is right now",
	Long: `Report whether you can actually GET an instance type — as opposed to whether it
exists and what it costs, which 'truffle find' already answers.

For scarce accelerator types the price is often not the deciding number: a type can
be listed, priced, and completely unobtainable. This gathers the four signals AWS
exposes and reports each one separately, because they fail independently and call
for different remedies:

  Spot placement    a 1-10 likelihood score per AZ (relative, not a guarantee)
  Offered AZs       where the type is offered at all, with the region's AZ count
  Quota headroom    vCPUs left under your On-Demand quota, and the quota to raise
  Capacity blocks   purchasable Capacity Block for ML offerings (24h window)

Each signal is best-effort: one being unavailable (commonly GetSpotPlacementScores,
which SCPs often deny) is reported as a warning, not a failure — an absent signal is
never presented as a good one.

Examples:
  truffle available p6-b200.48xlarge --region us-east-1
  truffle available p5.48xlarge --region us-west-2 --output json`,
	Args: cobra.ExactArgs(1),
	RunE: runAvailable,
}

func init() {
	rootCmd.AddCommand(availableCmd)

	availableCmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")
	availableCmd.ValidArgsFunction = completeInstanceType
}

func runAvailable(cmd *cobra.Command, args []string) error {
	instanceType := args[0]

	// One region per invocation: every signal here is region-specific, and the
	// answer for us-east-1 says nothing about us-west-2 (#109 records us-east-1 with
	// zero capacity-block offerings while us-west-2 had them for the same type).
	// Defaults to us-east-1 rather than fanning out across every region, since this
	// makes several API calls per region.
	region := "us-east-1"
	if len(regions) > 0 {
		region = regions[0]
		if len(regions) > 1 {
			fmt.Fprintf(os.Stderr, "Note: 'available' reports one region at a time; using %s.\n", region)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	awsClient, err := aws.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize AWS client: %w", err)
	}

	obt, err := awsClient.Obtainability(ctx, instanceType, region)
	if err != nil {
		return err
	}

	switch outputFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(obt)
	case "yaml":
		return yaml.NewEncoder(os.Stdout).Encode(obt)
	default:
		return printObtainability(os.Stdout, obt)
	}
}

// printObtainability renders the human-facing report. Each line states the signal
// and, where the value is a concern, why — a bare "1/10" or "0 vCPU" means nothing
// to someone who hasn't read the AWS docs for that API.
//
// The report is built in memory and written once, so a partially-rendered report
// can't reach the terminal on a write error.
func printObtainability(w io.Writer, o *aws.Obtainability) error {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	// rowf writes one aligned signal line. The error is dropped in exactly this one
	// place: the destination is an in-memory buffer, so a write cannot fail, and the
	// real write to w at the end IS checked.
	rowf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(tw, format, args...)
	}

	fmt.Fprintf(&b, "\nOBTAINABILITY  %s  %s\n\n", o.InstanceType, o.Region)

	var concerns []string

	// Spot placement.
	if best, ok := o.BestSpotPlacement(); ok {
		line := fmt.Sprintf("%d/10\t(%s / %s)", best.Score, best.AZID, best.AZ)
		rowf("  Spot placement\t%s\n", line)
		if best.Score <= 3 {
			concerns = append(concerns, "spot")
		}
	} else {
		rowf("  Spot placement\t—\t(no score available)\n")
	}

	// Offered AZs. The denominator is what makes this readable: "1 of 6" is a
	// concentration risk, "1 of 1" is just a small region.
	switch {
	case len(o.OfferedAZs) == 0:
		rowf("  Offered AZs\t0\t%s not offered in this region\n", i18n.Symbol("warning"))
		concerns = append(concerns, "azs")
	case o.TotalAZs > 0:
		note := ""
		if len(o.OfferedAZs) == 1 && o.TotalAZs > 1 {
			note = fmt.Sprintf("\t%s single-AZ footprint", i18n.Symbol("warning"))
			concerns = append(concerns, "azs")
		}
		rowf("  Offered AZs\t%d of %d%s\n", len(o.OfferedAZs), o.TotalAZs, note)
	default:
		rowf("  Offered AZs\t%d\n", len(o.OfferedAZs))
	}

	// Quota headroom. A zero quota is the single most common hard block, and it is
	// actionable — so name the quota code to request an increase against.
	if o.OnDemandQuotaHeadroom != nil {
		headroom := *o.OnDemandQuotaHeadroom
		detail := ""
		switch {
		case headroom == 0 && o.QuotaCode != "":
			detail = fmt.Sprintf("\t%s %s is 0", i18n.Symbol("warning"), o.QuotaCode)
			concerns = append(concerns, "quota")
		case headroom == 0:
			detail = fmt.Sprintf("\t%s no headroom", i18n.Symbol("warning"))
			concerns = append(concerns, "quota")
		default:
			if n, ok := o.InstanceHeadroom(); ok {
				detail = fmt.Sprintf("\t(%s)", pluralInstances(n))
				if n == 0 {
					// Headroom exists but not enough for even one of these.
					concerns = append(concerns, "quota")
				}
			}
		}
		rowf("  Quota headroom\t%d vCPU%s\n", headroom, detail)
	} else {
		rowf("  Quota headroom\t—\t(unavailable)\n")
	}

	// Capacity blocks. Counted at instance-count 1 (see #109) and over a stated
	// window, so the number can't be read as "never".
	if o.CapacityBlockOfferings != nil {
		window := ""
		if o.CapacityBlockWindowHours > 0 {
			window = fmt.Sprintf("\t(%dh window)", o.CapacityBlockWindowHours)
		}
		rowf("  Capacity blocks\t%s%s\n", pluralOfferings(*o.CapacityBlockOfferings), window)
	} else {
		rowf("  Capacity blocks\t—\t(unavailable)\n")
	}

	_ = tw.Flush()

	// The verdict names which signals drove it, so it can't be mistaken for an
	// opaque score. lagotto is the suggested remedy because waiting for capacity is
	// exactly what it does.
	fmt.Fprintln(&b)
	if len(concerns) > 0 {
		fmt.Fprintf(&b, "  → low obtainability (%s); consider `lagotto watch %s`\n",
			strings.Join(concerns, ", "), o.InstanceType)
	} else {
		fmt.Fprintf(&b, "  → no blocking signals found\n")
	}

	// Warnings last: a missing signal must be visible, since the reader would
	// otherwise take a blank as "fine".
	if len(o.Warnings) > 0 {
		fmt.Fprintln(&b)
		for _, warning := range o.Warnings {
			fmt.Fprintf(&b, "  ! %s\n", warning)
		}
	}
	fmt.Fprintln(&b)

	_, err := io.WriteString(w, b.String())
	return err
}

func pluralInstances(n int) string {
	if n == 1 {
		return "1 instance"
	}
	return fmt.Sprintf("%d instances", n)
}

func pluralOfferings(n int) string {
	if n == 1 {
		return "1 offering"
	}
	return fmt.Sprintf("%d offerings", n)
}

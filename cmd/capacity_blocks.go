package cmd

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
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
	cboDays          int
	cboStartDate     string
	cboStartAfter    string
	cboEndBy         string
	cboSort          string
)

var capacityBlocksCmd = &cobra.Command{
	Use:   "capacity-blocks",
	Short: "Discover purchasable EC2 Capacity Block for ML offerings",
	Long: `Discover purchasable EC2 Capacity Block for ML offerings (read-only).

This queries DescribeCapacityBlockOfferings — "what can I reserve?" — and shows
each offering's id, instance type/count, AZ, reservation window (in your local
timezone), duration, and up-front price. The offering id is what 'spawn
capacity-block purchase' reserves. Offerings are listed cheapest-first by default
(--sort start to order by start time instead).

Durations are day-granular: use --days (e.g. --days 1), or --duration-hours,
which is rounded up to a valid Capacity Block duration (1-day steps to 14 days,
then 7-day steps to 182). By default the search covers now → the soonest a block
of that duration could end; use --start-date / --start-after / --end-by to widen
or shift the window (blocks can start up to 8 weeks out).

For Capacity Blocks you ALREADY own, use 'truffle capacity --blocks' instead.

Examples:
  truffle capacity-blocks --instance-type p5.48xlarge --days 1
  truffle capacity-blocks --instance-type p5.48xlarge --start-date 2026-07-01 --days 2
  truffle capacity-blocks --instance-type p5.48xlarge --duration-hours 48 \
    --region us-east-1 --sort start --output json`,
	RunE: runCapacityBlocksOfferings,
}

func init() {
	rootCmd.AddCommand(capacityBlocksCmd)

	capacityBlocksCmd.Flags().StringVar(&cboInstanceType, "instance-type", "", "Instance type to find offerings for (required, e.g. p5.48xlarge)")
	capacityBlocksCmd.Flags().IntVar(&cboInstanceCount, "count", 1, "Number of instances in the block")
	capacityBlocksCmd.Flags().IntVar(&cboDurationHours, "duration-hours", 0, "Capacity Block duration in hours (e.g. 24); use --days for whole days")
	capacityBlocksCmd.Flags().IntVar(&cboDays, "days", 0, "Capacity Block duration in days (natural unit for CB-for-ML; e.g. --days 1). Overrides --duration-hours")
	capacityBlocksCmd.Flags().StringVar(&cboStartDate, "start-date", "", "Search for blocks starting on this calendar day (YYYY-MM-DD), in UTC")
	capacityBlocksCmd.Flags().StringVar(&cboStartAfter, "start-after", "", "Earliest block START time (RFC3339, e.g. 2026-07-01T00:00:00Z). Default: now")
	capacityBlocksCmd.Flags().StringVar(&cboEndBy, "end-by", "", "Latest block END time (RFC3339). Default: start + duration + 1d cushion")
	capacityBlocksCmd.Flags().StringVar(&cboSort, "sort", "price", "Sort offerings by: price (cheapest first) or start (soonest first)")
	capacityBlocksCmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for AWS API calls")
}

func runCapacityBlocksOfferings(cmd *cobra.Command, args []string) error {
	if cboInstanceType == "" {
		return fmt.Errorf("--instance-type is required (e.g. --instance-type p5.48xlarge)")
	}
	// --days is the natural unit for CB-for-ML (the console asks for days/weeks);
	// it just expands to hours. It takes precedence over --duration-hours.
	durationHours := cboDurationHours
	if cboDays > 0 {
		durationHours = cboDays * 24
	}
	if durationHours <= 0 {
		return fmt.Errorf("specify a duration with --days (e.g. --days 1) or --duration-hours (e.g. --duration-hours 24)")
	}
	// Normalize to a valid CB-for-ML duration (round up, with a notice) so an
	// arbitrary --duration-hours never produces AWS's opaque "duration is not valid"
	// error (#69). AWS accepts 1-day steps up to 14 days, then 7-day steps to 182.
	rounded, rerr := roundCapacityBlockDuration(durationHours)
	if rerr != nil {
		return rerr
	}
	if rounded != durationHours {
		fmt.Fprintf(os.Stderr, "Note: rounded duration up to %dh (%s) — Capacity Blocks for ML come in 1-day steps up to 14 days, then 7-day steps to 182 days.\n",
			rounded, humanizeHours(rounded))
		durationHours = rounded
	}
	if cboSort != "price" && cboSort != "start" {
		return fmt.Errorf("--sort must be 'price' or 'start' (got %q)", cboSort)
	}

	// Resolve the search window (#69). The API takes StartDateRange (earliest
	// start) and EndDateRange (latest END — NOT "starts before"). We expose:
	//   --start-after  → StartDateRange (honest: earliest start)
	//   --end-by       → EndDateRange   (honest: latest end; replaces the old,
	//                    mis-named --start-before)
	//   --start-date D → convenience: search blocks starting on calendar day D
	// and DEFAULT the window when the user gives none, so a bare invocation finds
	// near-term offerings instead of silently searching only "now".
	startAfter, endBy := cboStartAfter, cboEndBy
	if cboStartDate != "" {
		day, derr := time.Parse("2006-01-02", cboStartDate)
		if derr != nil {
			return fmt.Errorf("--start-date must be YYYY-MM-DD (got %q): %w", cboStartDate, derr)
		}
		day = day.UTC()
		if startAfter == "" {
			startAfter = day.Format(time.RFC3339)
		}
		if endBy == "" {
			// A block starting on day D runs durationHours and ends up to ~12h into
			// a later day (all blocks end 11:30 UTC), so the latest-end window must
			// cover D + duration + a 1-day cushion or the asked-for block is excluded.
			endBy = day.Add(time.Duration(durationHours)*time.Hour + 24*time.Hour).Format(time.RFC3339)
		}
	}
	// Default window: now → now + duration + cushion. Without this a bare query
	// searches only the immediate instant and misses near-future inventory (#69).
	if startAfter == "" {
		startAfter = nowUTC().Format(time.RFC3339)
	}
	if endBy == "" {
		endBy = nowUTC().Add(time.Duration(durationHours)*time.Hour + 24*time.Hour).Format(time.RFC3339)
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
		CapacityDurationHours: int32(durationHours),
		StartAfter:            startAfter,
		EndBy:                 endBy,
		Verbose:               verbose,
	})
	if err != nil {
		return i18n.Te("truffle.capacity.error.get_failed", err)
	}

	if len(results) == 0 {
		fmt.Println(i18n.T("truffle.capacity.no_results"))
		return nil
	}

	// Sort the offerings. Default is cheapest-first (--sort price), since the
	// up-front fee is usually the deciding factor; --sort start orders by the
	// reservation start time instead. Ties break on the other key then offering id
	// for a stable, predictable order.
	sortOfferings(results, cboSort)

	if outputFormat == "table" {
		fmt.Fprintf(os.Stderr, "Found %d Capacity Block offering(s) for %s ×%d (%dh). Purchase with: spawn capacity-block purchase <offering-id>\n\n",
			len(results), cboInstanceType, cboInstanceCount, durationHours)
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

// parseFee converts an offering's up-front fee string (e.g. "830.5900") to a
// float for numeric sorting. An unparseable/empty fee sorts last (+Inf) so real
// priced offerings always come first under --sort price.
func parseFee(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return math.Inf(1)
	}
	return f
}

// sortOfferings orders offerings by the chosen key. "price" sorts cheapest-first
// (up-front fee), tie-breaking on start then offering id; "start" sorts soonest-
// first, tie-breaking on fee then offering id. Both are fully deterministic.
func sortOfferings(results []aws.CapacityBlockOfferingResult, by string) {
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if by == "start" {
			if a.StartDate != b.StartDate {
				return a.StartDate < b.StartDate
			}
			if fa, fb := parseFee(a.UpfrontFee), parseFee(b.UpfrontFee); fa != fb {
				return fa < fb
			}
			return a.OfferingID < b.OfferingID
		}
		// default: price
		if fa, fb := parseFee(a.UpfrontFee), parseFee(b.UpfrontFee); fa != fb {
			return fa < fb
		}
		if a.StartDate != b.StartDate {
			return a.StartDate < b.StartDate
		}
		return a.OfferingID < b.OfferingID
	})
}

// nowUTC returns the current UTC time. Wrapped so tests can reason about the
// default window without monkey-patching the clock everywhere.
func nowUTC() time.Time { return time.Now().UTC() }

// maxCapacityBlockHours is the AWS ceiling for a CB-for-ML reservation: 182 days.
const maxCapacityBlockHours = 182 * 24

// roundCapacityBlockDuration normalizes an arbitrary hour count UP to the next
// valid CB-for-ML duration (#69). AWS accepts durations in 1-day increments up to
// 14 days, then 7-day increments up to 182 days. Anything in between rounds up to
// the next valid step; anything over 182 days is rejected. Already-valid values
// pass through unchanged.
func roundCapacityBlockDuration(hours int) (int, error) {
	if hours <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	if hours > maxCapacityBlockHours {
		return 0, fmt.Errorf("duration %dh exceeds the Capacity Block maximum of 182 days (%dh)", hours, maxCapacityBlockHours)
	}
	const day = 24
	const week = 7 * day
	const fourteenDays = 14 * day
	if hours <= fourteenDays {
		// Round up to the next whole day.
		return ((hours + day - 1) / day) * day, nil
	}
	// Beyond 14 days: 7-day steps measured from the 14-day mark.
	over := hours - fourteenDays
	steps := (over + week - 1) / week
	return fourteenDays + steps*week, nil
}

// humanizeHours renders a valid CB-for-ML duration as a friendly day/week string
// for the round-up notice (e.g. 48 → "2 days", 504 → "3 weeks").
func humanizeHours(hours int) string {
	if hours%(7*24) == 0 {
		w := hours / (7 * 24)
		if w == 1 {
			return "1 week"
		}
		return fmt.Sprintf("%d weeks", w)
	}
	d := hours / 24
	if d == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", d)
}

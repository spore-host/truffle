package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/awscfg"
)

// Region resolution shared by every multi-region command (#188, #194).
//
// Each command that fans out across regions declares a regionDefault — what it
// searches when the user gives neither --regions nor --all-regions — and
// resolves its region list through commandRegions. The flags behave the same
// everywhere:
//
//   - --regions / --region: exactly those regions (always wins);
//   - --all-regions: every enabled region;
//   - both together: an error;
//   - neither: the command's regionDefault.
//
// Which default a command gets is a per-command decision based on its purpose;
// see the regionDefault constants and each command's registration.

// regionDefault is what a command searches when no region flag is given.
type regionDefault int

const (
	// defaultConfiguredRegion: search the configured default region (spore
	// config, AWS_REGION / AWS_DEFAULT_REGION, or the profile's region), and
	// fall back to every enabled region only if none is configured. For
	// "describe this instance type" lookups (find, search), where most users
	// launch in one region and a global fan-out is slow (#188).
	defaultConfiguredRegion regionDefault = iota
	// defaultAllRegions: search every enabled region. For commands whose
	// answer is a cross-region comparison or inventory (spot: cheapest region;
	// az: which regions have enough AZs; capacity: your reservations wherever
	// they are; capacity-blocks: which region has offerings), where one region
	// would silently give a partial answer.
	defaultAllRegions
)

// Per-command region defaults (#194). Each multi-region command uses its
// constant both to register --all-regions and to resolve its regions, so this
// block is the single record of the decision.
const (
	// find: describe matching instance types; most users launch in one region.
	findRegionDefault = defaultConfiguredRegion
	// search: find's deprecated pattern-only predecessor; same purpose.
	searchRegionDefault = defaultConfiguredRegion
	// spot: find the cheapest region/AZ, which needs every region.
	spotRegionDefault = defaultAllRegions
	// az: which regions offer the type in enough AZs (--min-az-count,
	// --regions-only); a cross-region comparison.
	azRegionDefault = defaultAllRegions
	// capacity: inventory of reservations you own, which can be in any region.
	capacityRegionDefault = defaultAllRegions
	// capacity-blocks: which region has a purchasable block; offerings are
	// region-gated (#109).
	capacityBlocksRegionDefault = defaultAllRegions
)

// regionScope records how a command arrived at its region list, so it can tell
// the user what is being searched.
type regionScope int

const (
	// scopeExplicit: the user passed --regions / --region (or, for az, --az).
	scopeExplicit regionScope = iota
	// scopeConfigured: no flag; searching the configured default region.
	scopeConfigured
	// scopeAll: the user passed --all-regions.
	scopeAll
	// scopeAllDefault: no flag; the command defaults to every enabled region.
	scopeAllDefault
	// scopeAllFallback: no flag, the command defaults to the configured region,
	// but none is configured anywhere, so every enabled region is searched.
	scopeAllFallback
)

// allRegions backs the --all-regions flag. Each multi-region command registers
// its own local --all-regions (via addAllRegionsFlag) bound to this variable,
// so the flag only appears on commands it applies to.
var allRegions bool

// errRegionsConflict is returned when --regions and --all-regions are both
// given: they ask for contradictory scopes, and silently picking one would hide
// the mistake.
var errRegionsConflict = errors.New("--all-regions cannot be combined with --regions/--region: pass one or the other")

// addAllRegionsFlag registers --all-regions on cmd, with help text that states
// the command's default scope.
func addAllRegionsFlag(cmd *cobra.Command, def regionDefault) {
	usage := "Search every enabled region (default: only your configured region, e.g. AWS_REGION or the profile's region)"
	if def == defaultAllRegions {
		usage = "Search every enabled region (already the default here; useful to be explicit in scripts). Cannot be combined with --regions"
	}
	cmd.Flags().BoolVar(&allRegions, "all-regions", false, usage)
}

// resolveRegions decides which regions a command searches:
//
//   - explicit regions always win (and conflict with --all-regions);
//   - --all-regions searches every enabled region;
//   - otherwise the command's default: every enabled region, or the configured
//     region with a fallback to every enabled region when none is configured.
//
// listEnabled is only called when every enabled region is actually needed, so
// the single-region path skips the DescribeRegions round trip.
func resolveRegions(ctx context.Context, explicit []string, all bool, def regionDefault, configured string, listEnabled func(context.Context) ([]string, error)) ([]string, regionScope, error) {
	if len(explicit) > 0 {
		if all {
			return nil, scopeExplicit, errRegionsConflict
		}
		return explicit, scopeExplicit, nil
	}

	var scope regionScope
	switch {
	case all:
		scope = scopeAll
	case def == defaultAllRegions:
		scope = scopeAllDefault
	case configured != "":
		return []string{configured}, scopeConfigured, nil
	default:
		scope = scopeAllFallback
	}

	enabled, err := listEnabled(ctx)
	if err != nil {
		return nil, scope, i18n.Te("truffle.search.error.get_regions_failed", err)
	}
	return enabled, scope, nil
}

// printRegionScope tells the user (table output only, on stderr) what scope a
// configured-region-default command is searching and how to change it.
// Commands that default to every enabled region print nothing: searching
// everywhere is what they are for, and they never did.
func printRegionScope(w io.Writer, def regionDefault, scope regionScope, searchRegions []string) {
	if outputFormat != "table" || def == defaultAllRegions {
		return
	}
	switch scope {
	case scopeConfigured:
		_, _ = fmt.Fprintf(w, "%s Searching your configured region (%s). Use --all-regions to search every enabled region, or --regions to choose.\n\n",
			i18n.Emoji("globe"), searchRegions[0])
	case scopeAll:
		if len(searchRegions) > 10 {
			_, _ = fmt.Fprintf(w, "%s Searching across all %d enabled regions (this may take a while)\n",
				i18n.Emoji("warning"), len(searchRegions))
			_, _ = fmt.Fprintf(w, "   Tip: omit --all-regions to search only your configured region, or use --regions (e.g., --regions us-east-1,us-west-2)\n\n")
		}
	case scopeAllFallback:
		_, _ = fmt.Fprintf(w, "%s No default AWS region configured; searching all %d enabled regions (this may take a while)\n",
			i18n.Emoji("warning"), len(searchRegions))
		_, _ = fmt.Fprintf(w, "   Tip: set AWS_REGION (or a profile/spore config region), or use --regions (e.g., --regions us-east-1,us-west-2)\n\n")
	}
}

// regionClient is the slice of *aws.Client that region resolution needs.
type regionClient interface {
	ConfiguredRegion() string
	GetEnabledRegions(ctx context.Context) ([]string, error)
}

// commandRegions resolves and announces a command's region scope from the
// shared --regions / --all-regions flags.
func commandRegions(ctx context.Context, client regionClient, def regionDefault) ([]string, error) {
	return commandRegionsFrom(ctx, client, def, regions)
}

// commandRegionsFrom is commandRegions with the explicit region list supplied
// by the caller (az derives it from --az).
func commandRegionsFrom(ctx context.Context, client regionClient, def regionDefault, explicit []string) ([]string, error) {
	configured := client.ConfiguredRegion()
	if verbose && len(explicit) == 0 && (allRegions || def == defaultAllRegions || configured == "") {
		fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("globe"), i18n.T("truffle.search.fetching_regions"))
	}
	searchRegions, scope, err := resolveRegions(ctx, explicit, allRegions, def, configured, client.GetEnabledRegions)
	if err != nil {
		return nil, err
	}
	printRegionScope(os.Stderr, def, scope, searchRegions)
	return searchRegions, nil
}

// singleRegionFallback is the region quotas and available check when no region
// flag is given and none is configured anywhere (#200). They query regional
// APIs (Service Quotas, EC2), so they need some real region; us-east-1 is the
// documented last resort, never a default that overrides configuration.
const singleRegionFallback = "us-east-1"

// configuredOrFallbackRegions resolves the regions for commands that check one
// region by default rather than fanning out (quotas, available, list; #200,
// #202):
//
//   - explicit regions (--regions / --region) always win;
//   - otherwise the configured region (spore config, AWS_REGION /
//     AWS_DEFAULT_REGION, profile region), as find and search use;
//   - otherwise us-east-1, with fellBack reporting that nothing was configured.
func configuredOrFallbackRegions(explicit []string, configured string) (searchRegions []string, fellBack bool) {
	if len(explicit) > 0 {
		return explicit, false
	}
	if configured != "" {
		return []string{configured}, false
	}
	return []string{singleRegionFallback}, true
}

// singleRegion picks the one region a single-region command (available, list)
// queries: the first explicit region, else the configured region, else the
// us-east-1 fallback (#200, #202). Notes go to w (stderr): which region is used
// when several were given, and that the fallback is in use.
func singleRegion(w io.Writer, command string, explicit []string, configured string) string {
	candidates, fellBack := configuredOrFallbackRegions(explicit, configured)
	region := candidates[0]
	if len(candidates) > 1 {
		_, _ = fmt.Fprintf(w, "Note: '%s' reports one region at a time; using %s.\n", command, region)
	}
	if fellBack {
		printRegionFallback(w)
	}
	return region
}

// printRegionFallback tells the user (on stderr) that no region is configured
// and the us-east-1 fallback is in use, so a quota or obtainability answer is
// never silently about a region they don't launch in.
func printRegionFallback(w io.Writer) {
	_, _ = fmt.Fprintf(w, "%s No default AWS region configured; using %s. Set AWS_REGION (or a profile/spore config region), or pass --region.\n",
		i18n.Emoji("warning"), singleRegionFallback)
}

// configuredRegion returns the default region the shared spore.host / AWS
// config resolves to, or "" when none is set: the same lookup
// (*aws.Client).ConfiguredRegion uses.
func configuredRegion(ctx context.Context) (string, error) {
	cfg, err := awscfg.Load(ctx, "")
	if err != nil {
		return "", err
	}
	return cfg.Region, nil
}

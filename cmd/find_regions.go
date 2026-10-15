package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spore-host/libs/i18n"
)

// findRegionScope records how `truffle find` arrived at its region list, so the
// caller can tell the user what is being searched (#188).
type findRegionScope int

const (
	// findScopeExplicit: the user passed --regions / --region.
	findScopeExplicit findRegionScope = iota
	// findScopeConfigured: no flag given; searching the configured default
	// region (spore config, AWS_REGION / AWS_DEFAULT_REGION, or profile region).
	findScopeConfigured
	// findScopeAll: the user asked for --all-regions.
	findScopeAll
	// findScopeAllFallback: no flag given and no region configured anywhere, so
	// find falls back to every enabled region (the pre-#188 default).
	findScopeAllFallback
)

// errFindRegionsConflict is returned when --regions and --all-regions are both
// given: they ask for contradictory scopes, and silently picking one would hide
// the mistake.
var errFindRegionsConflict = errors.New("--all-regions cannot be combined with --regions/--region: pass one or the other")

// resolveFindRegions decides which regions `truffle find` searches (#188):
//
//   - explicit --regions always win (and conflict with --all-regions);
//   - --all-regions searches every enabled region;
//   - otherwise the configured default region alone;
//   - with no configured region, fall back to every enabled region.
//
// listEnabled is only called when every enabled region is actually needed, so
// the common single-region path skips the DescribeRegions round trip.
func resolveFindRegions(ctx context.Context, explicit []string, allRegions bool, configured string, listEnabled func(context.Context) ([]string, error)) ([]string, findRegionScope, error) {
	if len(explicit) > 0 {
		if allRegions {
			return nil, findScopeExplicit, errFindRegionsConflict
		}
		return explicit, findScopeExplicit, nil
	}

	scope := findScopeAll
	if !allRegions {
		if configured != "" {
			return []string{configured}, findScopeConfigured, nil
		}
		scope = findScopeAllFallback
	}

	all, err := listEnabled(ctx)
	if err != nil {
		return nil, scope, i18n.Te("truffle.search.error.get_regions_failed", err)
	}
	return all, scope, nil
}

// printFindRegionScope tells the user (table output only, on stderr) what scope
// find is searching and how to change it.
func printFindRegionScope(w io.Writer, scope findRegionScope, searchRegions []string) {
	if outputFormat != "table" {
		return
	}
	switch scope {
	case findScopeConfigured:
		_, _ = fmt.Fprintf(w, "%s Searching your configured region (%s). Use --all-regions to search every enabled region, or --regions to choose.\n\n",
			i18n.Emoji("globe"), searchRegions[0])
	case findScopeAll:
		if len(searchRegions) > 10 {
			_, _ = fmt.Fprintf(w, "%s Searching across all %d enabled regions (this may take a while)\n",
				i18n.Emoji("warning"), len(searchRegions))
			_, _ = fmt.Fprintf(w, "   Tip: omit --all-regions to search only your configured region, or use --regions (e.g., --regions us-east-1,us-west-2)\n\n")
		}
	case findScopeAllFallback:
		_, _ = fmt.Fprintf(w, "%s No default AWS region configured; searching all %d enabled regions (this may take a while)\n",
			i18n.Emoji("warning"), len(searchRegions))
		_, _ = fmt.Fprintf(w, "   Tip: set AWS_REGION (or a profile/spore config region), or use --regions (e.g., --regions us-east-1,us-west-2)\n\n")
	}
}

// findRegionClient is the slice of *aws.Client that find's region resolution needs.
type findRegionClient interface {
	ConfiguredRegion() string
	GetEnabledRegions(ctx context.Context) ([]string, error)
}

// findSearchRegions resolves and announces find's region scope.
func findSearchRegions(ctx context.Context, client findRegionClient) ([]string, error) {
	if verbose && len(regions) == 0 && (findAllRegions || client.ConfiguredRegion() == "") {
		fmt.Fprintf(os.Stderr, "%s %s\n", i18n.Emoji("globe"), i18n.T("truffle.search.fetching_regions"))
	}
	searchRegions, scope, err := resolveFindRegions(ctx, regions, findAllRegions, client.ConfiguredRegion(), client.GetEnabledRegions)
	if err != nil {
		return nil, err
	}
	printFindRegionScope(os.Stderr, scope, searchRegions)
	return searchRegions, nil
}

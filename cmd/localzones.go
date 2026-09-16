package cmd

import (
	"context"

	"github.com/spore-host/truffle/pkg/aws"
)

// classifyLocalZoneAZs returns the set of AZ names among the results that are
// Local Zones or Wavelength Zones, classified by their authoritative
// DescribeAvailabilityZones ZoneType (#164) so find/search/az can label them in
// the AZ column.
//
// Best-effort by design: it only consults regions whose results actually carry
// AZ data (so --skip-azs / no-AZ output adds no API calls), and a region whose
// zone lookup fails is simply skipped — its AZs go unlabeled rather than turning
// a working search into an error. Zone classification is memoized on the client,
// so this costs at most one DescribeAvailabilityZones call per region. Returns
// nil when there's nothing to label.
func classifyLocalZoneAZs(ctx context.Context, client *aws.Client, results []aws.InstanceTypeResult) map[string]bool {
	regionsWithAZs := make(map[string]bool)
	for _, r := range results {
		if len(r.AvailableAZs) > 0 {
			regionsWithAZs[r.Region] = true
		}
	}
	if len(regionsWithAZs) == 0 {
		return nil
	}

	local := make(map[string]bool)
	for region := range regionsWithAZs {
		zones, err := client.ZoneInfos(ctx, region)
		if err != nil {
			continue // degrade gracefully: leave this region's AZs unlabeled
		}
		for name, info := range zones {
			if info.IsExtended() {
				local[name] = true
			}
		}
	}
	if len(local) == 0 {
		return nil
	}
	return local
}

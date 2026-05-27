package spawn

// SpawnSupportedRegions lists AWS regions where spawn is fully operational.
// These regions have spored binaries deployed to S3 and all infrastructure available.
var SpawnSupportedRegions = map[string]bool{
	"us-east-1":      true,
	"us-east-2":      true,
	"us-west-1":      true,
	"us-west-2":      true,
	"ca-central-1":   true,
	"eu-west-1":      true,
	"eu-west-2":      true,
	"eu-central-1":   true,
	"ap-southeast-1": true,
	"ap-southeast-2": true,
	"ap-northeast-1": true,
}

// IsSpawnSupported returns true if spawn can launch instances in the given region.
func IsSpawnSupported(region string) bool {
	return SpawnSupportedRegions[region]
}

// SpawnSupportedRegionsList returns a sorted list of spawn-supported regions.
func SpawnSupportedRegionsList() []string {
	regions := make([]string, 0, len(SpawnSupportedRegions))
	for region := range SpawnSupportedRegions {
		regions = append(regions, region)
	}
	return regions
}

package spawn

import "testing"

func TestIsSpawnSupported(t *testing.T) {
	tests := []struct {
		region string
		want   bool
	}{
		{"us-east-1", true},
		{"us-west-2", true},
		{"eu-central-1", true},
		{"ap-southeast-2", true},
		{"af-south-1", false},
		{"me-south-1", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSpawnSupported(tt.region); got != tt.want {
			t.Errorf("IsSpawnSupported(%q) = %v, want %v", tt.region, got, tt.want)
		}
	}
}

func TestSpawnSupportedRegionsList(t *testing.T) {
	regions := SpawnSupportedRegionsList()
	if len(regions) != len(SpawnSupportedRegions) {
		t.Errorf("got %d regions, want %d", len(regions), len(SpawnSupportedRegions))
	}
	for _, r := range regions {
		if !SpawnSupportedRegions[r] {
			t.Errorf("region %q in list but not in map", r)
		}
	}
}

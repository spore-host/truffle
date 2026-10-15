package find

import (
	"context"
	"slices"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/aws/awsmock"
)

// TestAcceleratorNominalSearch checks queries written in AWS's documented
// per-device sizes find their types (#242): a B200 is 179 GB and a B300 268
// GB (AWS's per-instance totals over 8 GPUs), and a Trainium2 chip 96 GB, so
// "trainium2 256gb" no longer matches trn2 on EC2's 512 GiB per device.
// AccelMemoryMiB is as buildResultFromEC2 records it.
func TestAcceleratorNominalSearch(t *testing.T) {
	mk := func(it string, accelMiB int64) aws.InstanceTypeResult {
		return aws.InstanceTypeResult{
			InstanceType: it, Region: "us-east-1", VCPUs: 192, MemoryMiB: 2048 * 1024,
			Architecture: "x86_64", AccelMemoryMiB: accelMiB, GPUMemoryPerMiB: accelMiB,
		}
	}
	catalog := []aws.InstanceTypeResult{
		mk("p6-b200.48xlarge", 183359),
		mk("p6-b300.48xlarge", 275040),
		mk("trn2.48xlarge", 98304),
	}
	for _, tt := range []struct {
		query string
		exact bool
		want  []string
	}{
		{"b200 179gb", false, []string{"p6-b200.48xlarge"}},
		{"b200 179gb", true, []string{"p6-b200.48xlarge"}},
		{"b300 268gb", true, []string{"p6-b300.48xlarge"}},
		{"trainium2 96gb", true, []string{"trn2.48xlarge"}},
		{"trainium2 256gb", false, nil},
	} {
		pq := mustParse(t, tt.query)
		pq.ExactMatch = tt.exact
		sc, err := pq.BuildCriteria(false)
		if err != nil {
			t.Fatal(err)
		}
		f := &awsmock.Finder{Instances: catalog}
		rs, err := f.SearchInstanceTypes(context.Background(), []string{"us-east-1"}, sc.InstanceTypePattern, sc.FilterOptions)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rs {
			got = append(got, r.InstanceType)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q (exact %v) = %v, want %v", tt.query, tt.exact, got, tt.want)
		}
	}
}

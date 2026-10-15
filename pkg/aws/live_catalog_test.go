package aws

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/metadata"
)

// liveCatalog returns every instance type DescribeInstanceTypes reports in
// the regions named by TRUFFLE_LIVE_REGIONS (default us-west-2,us-east-1).
// It skips unless TRUFFLE_LIVE_AWS=1, since it needs AWS credentials; it only
// reads.
func liveCatalog(t *testing.T) []types.InstanceTypeInfo {
	t.Helper()
	if os.Getenv("TRUFFLE_LIVE_AWS") != "1" {
		t.Skip("set TRUFFLE_LIVE_AWS=1 to check against the live EC2 catalog (read-only)")
	}
	regions := []string{"us-west-2", "us-east-1"}
	if v := os.Getenv("TRUFFLE_LIVE_REGIONS"); v != "" {
		regions = strings.Split(v, ",")
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	var all []types.InstanceTypeInfo
	for _, region := range regions {
		c := cfg.Copy()
		c.Region = region
		p := ec2.NewDescribeInstanceTypesPaginator(ec2.NewFromConfig(c), &ec2.DescribeInstanceTypesInput{})
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				t.Fatalf("DescribeInstanceTypes in %s: %v", region, err)
			}
			all = append(all, page.InstanceTypes...)
		}
	}
	if len(all) == 0 {
		t.Fatal("live catalog is empty; this check would pass vacuously")
	}
	return all
}

// TestLiveSizesHaveACategory: every size EC2 offers today is in some size
// category, so some query word selects it (#220).
func TestLiveSizesHaveACategory(t *testing.T) {
	missing := map[string]string{}
	for _, it := range liveCatalog(t) {
		name := string(it.InstanceType)
		size := name[strings.LastIndexByte(name, '.')+1:]
		if metadata.SizeCategoryOf(size) == "" {
			missing[size] = name
		}
	}
	for size, example := range missing {
		t.Errorf("size %q (e.g. %s) is in no size category", size, example)
	}
}

// TestLiveGravitonFamiliesAreListed: every family whose processor AWS makes
// is in a Graviton processor entry, and no listed Graviton family is made by
// someone else (#221). The processor lists can't be derived from the API (it
// reports the manufacturer, not the Graviton generation), so this is how a
// new family such as r9g gets noticed.
func TestLiveGravitonFamiliesAreListed(t *testing.T) {
	listed := make(map[string]bool)
	for _, f := range metadata.GetFamiliesByVendor("aws") {
		listed[f] = true
	}
	live := make(map[string]string) // family -> manufacturer
	for _, it := range liveCatalog(t) {
		name := string(it.InstanceType)
		family := name[:strings.IndexByte(name, '.')]
		if it.ProcessorInfo != nil && it.ProcessorInfo.Manufacturer != nil {
			live[family] = *it.ProcessorInfo.Manufacturer
		}
	}
	for family, maker := range live {
		if maker == "AWS" && !listed[family] {
			t.Errorf("%s has an AWS (Graviton) processor but is in no Graviton processor entry", family)
		}
	}
	for family := range listed {
		if maker, ok := live[family]; ok && maker != "AWS" {
			t.Errorf("%s is listed as Graviton but its processor is made by %s", family, maker)
		}
	}
}

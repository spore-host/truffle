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

// TestLiveVendorFamiliesAreListed: every family whose processor AWS, AMD or
// Intel makes is selected by that vendor's word, and no family a vendor word
// selects is made by someone else (#221, #228). Vendor membership is a
// recorded snapshot of ProcessorInfo.Manufacturer (the API has no codename to
// derive the processor entries from), so this is how a new family such as
// r9g or x8aedz gets noticed.
func TestLiveVendorFamiliesAreListed(t *testing.T) {
	vendorOf := map[string]string{"AWS": "aws", "AMD": "amd", "Intel": "intel"}
	listed := make(map[string]string) // family -> vendor word that selects it
	for _, v := range []string{"aws", "amd", "intel"} {
		for _, f := range metadata.GetFamiliesByVendor(v) {
			if prev, ok := listed[f]; ok {
				t.Errorf("%s is selected by both %q and %q", f, prev, v)
			}
			listed[f] = v
		}
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
		want, ok := vendorOf[maker]
		if !ok {
			continue // e.g. Apple: no vendor word
		}
		if got := listed[family]; got != want {
			t.Errorf("%s has a %s processor but is selected by %q, want %q", family, maker, got, want)
		}
	}
	for family, v := range listed {
		if maker, ok := live[family]; ok && vendorOf[maker] != v {
			t.Errorf("%s is selected by %q but its processor is made by %s", family, v, maker)
		}
	}
}

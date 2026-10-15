package aws

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

// Patterns below are verbatim BuildCriteria output for real queries (family
// order varies run to run because it comes from a map). pkg/find imports this
// package, so the test can't call BuildCriteria itself.
const (
	patTurin    = `^(m8azn|c8a|r8a|m8a)\..*$`                     // "amd turin 16 cores 64gb"
	patGraviton = `^(c6g|c6gd|c6gn|m7g|m7gd|a1|t4g|r8g|c8g)\..*$` // "graviton 8 cores 32gb" (subset)
	patH100     = `^(p5\.48xlarge)$`                              // "h100"
	patGPUs     = `^(p4d\.24xlarge|p4de\.24xlarge|p5\.48xlarge)$` // "a100 h100"
	patH100EFA  = `^(p5)\..*$`                                    // "h100 efa"
	patSmall    = `^(m7i|m7i-flex|t2|c7i)\.(small|medium)$`       // "intel small" (subset)
	patNvidia   = `^(p5|p5e|p5en|p6-b200|g5|g6|g6e)\..*$`         // "nvidia" (subset)
	patGlob     = `^m7i.*$`                                       // glob "m7i*"
	patAll      = `.*`                                            // "16 cores 64gb"
)

func TestInstanceTypeFilterValues(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{patTurin, []string{"m8azn.*", "c8a.*", "r8a.*", "m8a.*"}},
		{patH100, []string{"p5.48xlarge"}},
		{patGPUs, []string{"p4d.24xlarge", "p4de.24xlarge", "p5.48xlarge"}},
		{patH100EFA, []string{"p5.*"}},
		{patSmall, []string{"m7i.*", "m7i-flex.*", "t2.*", "c7i.*"}},
		{`^(p6-b200|g5)\..*$`, []string{"p6-b200.*", "g5.*"}},
		{patGlob, []string{"m7i*"}},
		{`^c7i\.large$`, []string{"c7i.large"}},
		{`^(c7i|c7i)\..*$`, []string{"c7i.*"}},

		// Not pushed down: no anchored literal prefix, or a shape whose
		// prefix doesn't bound every match.
		{patAll, nil},
		{`^.*$`, nil},
		{`^$`, nil},
		{`m7i.*`, nil},                  // unanchored
		{`^m7i.*`, nil},                 // no trailing anchor
		{`^(c7i|m7i)\..*|t3\..*$`, nil}, // top-level alternation escapes the prefix
		{`^m7i|t3$`, nil},
		{`^(c7i|m7i)?\.large$`, nil}, // optional group
		{`^m7i*$`, nil},              // quantifier on the prefix's last char
		{`^m7i+\.large$`, nil},
		{`^(c7i|m.i)\..*$`, nil},   // regex inside the group
		{`^(c7i|(m7i))\..*$`, nil}, // nested group
		{`^(?i)m7i.*$`, nil},       // flags
		{`^(c7i\)|m7i)\..*$`, nil}, // escaped paren inside the group
		{`^(c7i|)\..*$`, nil},      // empty alternative
		{`^\d+\..*$`, nil},
		{`^c[67]i\..*$`, []string{"c*"}}, // prefix "c", class narrows client-side
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			regexp.MustCompile(tt.pattern) // every case must be a valid regex
			got := instanceTypeFilterValues(tt.pattern)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("instanceTypeFilterValues(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestDescribeInputsFor(t *testing.T) {
	t.Run("family pattern and architecture", func(t *testing.T) {
		inputs := describeInputsFor(regexp.MustCompile(patTurin), FilterOptions{Architecture: "x86_64", MinVCPUs: 16, MinMemory: 64})
		if len(inputs) != 1 {
			t.Fatalf("got %d inputs, want 1", len(inputs))
		}
		got := filterMap(inputs[0].Filters)
		want := map[string][]string{
			"instance-type":                         {"m8azn.*", "c8a.*", "r8a.*", "m8a.*"},
			"processor-info.supported-architecture": {"x86_64"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filters = %v, want %v (minimum vCPU/memory must stay client-side)", got, want)
		}
	})

	t.Run("exact vCPUs pushed down", func(t *testing.T) {
		inputs := describeInputsFor(regexp.MustCompile(patAll), FilterOptions{MinVCPUs: 16, ExactVCPUs: true, MinMemory: 64, ExactMemory: true})
		got := filterMap(inputs[0].Filters)
		want := map[string][]string{"vcpu-info.default-vcpus": {"16"}}
		if len(inputs) != 1 || !reflect.DeepEqual(got, want) {
			t.Errorf("inputs=%d filters=%v, want 1 input with %v", len(inputs), got, want)
		}
	})

	t.Run("nothing to push down", func(t *testing.T) {
		for _, m := range []*regexp.Regexp{nil, regexp.MustCompile(patAll)} {
			inputs := describeInputsFor(m, FilterOptions{MinVCPUs: 4})
			if len(inputs) != 1 || len(inputs[0].Filters) != 0 || len(inputs[0].InstanceTypes) != 0 {
				t.Errorf("matcher %v: got %+v, want one unfiltered input", m, inputs)
			}
		}
	})

	t.Run("chunked under the filter-value limit", func(t *testing.T) {
		fams := make([]string, 450)
		for i := range fams {
			fams[i] = fmt.Sprintf("f%d", i)
		}
		m := regexp.MustCompile(`^(` + strings.Join(fams, "|") + `)\..*$`)
		inputs := describeInputsFor(m, FilterOptions{Architecture: "arm64"})
		if len(inputs) != 3 {
			t.Fatalf("got %d inputs, want 3", len(inputs))
		}
		total := 0
		for _, in := range inputs {
			n := 0
			for _, f := range in.Filters {
				n += len(f.Values)
			}
			if n > maxFilterValuesPerCall {
				t.Errorf("input has %d filter values, limit %d", n, maxFilterValuesPerCall)
			}
			total += len(filterMap(in.Filters)["instance-type"])
		}
		if total != len(fams) {
			t.Errorf("chunks cover %d families, want %d", total, len(fams))
		}
	})
}

// TestMatchingInstanceTypes_SameAsFullScan is the #195 equivalence check:
// for every query shape, narrowing server-side must return exactly what the
// old full-catalog scan returned, while fetching fewer types where a
// constraint can be pushed down.
func TestMatchingInstanceTypes_SameAsFullScan(t *testing.T) {
	catalog := fixtureCatalog()

	tests := []struct {
		name     string
		pattern  string
		opts     FilterOptions
		narrowed bool // whether server-side filters must cut the fetched set
	}{
		{"turin 16 cores 64gb", patTurin, FilterOptions{Architecture: "x86_64", MinVCPUs: 16, MinMemory: 64}, true},
		{"turin exact", patTurin, FilterOptions{Architecture: "x86_64", MinVCPUs: 16, MinMemory: 64, ExactVCPUs: true, ExactMemory: true}, true},
		{"graviton 8 cores 32gb", patGraviton, FilterOptions{Architecture: "arm64", MinVCPUs: 8, MinMemory: 32}, true},
		{"graviton physical cores", patGraviton, FilterOptions{Architecture: "arm64", MinPhysicalCores: 8}, true},
		{"h100", patH100, FilterOptions{}, true},
		{"a100 h100", patGPUs, FilterOptions{}, true},
		{"h100 efa", patH100EFA, FilterOptions{}, true},
		{"intel small", patSmall, FilterOptions{Architecture: "x86_64"}, true},
		{"nvidia", patNvidia, FilterOptions{}, true},
		{"glob m7i*", patGlob, FilterOptions{}, true},
		{"x86_64 4 cores", patAll, FilterOptions{Architecture: "x86_64", MinPhysicalCores: 4}, true},
		{"i386-capable x86", patAll, FilterOptions{Architecture: "x86_64", MinVCPUs: 1}, true},
		{"exact 8 vCPU", patAll, FilterOptions{MinVCPUs: 8, ExactVCPUs: true}, true},
		{"16 cores 64gb", patAll, FilterOptions{MinVCPUs: 16, MinMemory: 64}, false},
		{"nested virt", patAll, FilterOptions{NestedVirt: true}, false},
		{"no match", `^$`, FilterOptions{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := regexp.MustCompile(tt.pattern)

			// Reference: the pre-#195 behaviour — fetch the whole catalog and
			// filter client-side.
			var want []string
			for _, it := range catalog {
				if m.MatchString(string(it.InstanceType)) && matchesFilters(it, tt.opts) {
					want = append(want, string(it.InstanceType))
				}
			}

			fake := &fakeEC2{catalog: catalog}
			matched, err := matchingInstanceTypes(context.Background(), fake.describe, m, tt.opts)
			if err != nil {
				t.Fatalf("matchingInstanceTypes: %v", err)
			}
			var got []string
			for _, it := range matched {
				got = append(got, string(it.InstanceType))
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("results differ from full scan\n got: %v\nwant: %v", got, want)
			}
			if tt.narrowed && fake.fetched >= len(catalog) {
				t.Errorf("fetched %d of %d catalog types; want the query narrowed server-side", fake.fetched, len(catalog))
			}
		})
	}
}

// TestMatchingInstanceTypes_NilMatcher: a nil matcher still means "all types
// that pass opts" (#106), with only opts pushed down.
func TestMatchingInstanceTypes_NilMatcher(t *testing.T) {
	fake := &fakeEC2{catalog: fixtureCatalog()}
	got, err := matchingInstanceTypes(context.Background(), fake.describe, nil, FilterOptions{Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no arm64 types returned")
	}
	for _, it := range got {
		if !matchesFilters(it, FilterOptions{Architecture: "arm64"}) {
			t.Errorf("%s is not arm64", it.InstanceType)
		}
	}
}

// fakeEC2 serves DescribeInstanceTypes over a fixture catalog, applying the
// subset of EC2 filter semantics truffle pushes down, and counts how many
// types it returned.
type fakeEC2 struct {
	catalog []types.InstanceTypeInfo
	fetched int
}

func (f *fakeEC2) describe(_ context.Context, in *ec2.DescribeInstanceTypesInput) ([]types.InstanceTypeInfo, error) {
	n := 0
	for _, fl := range in.Filters {
		n += len(fl.Values)
	}
	if n > maxFilterValuesPerCall {
		return nil, &smithy.GenericAPIError{Code: "FilterLimitExceeded"}
	}

	var out []types.InstanceTypeInfo
	for _, it := range f.catalog {
		if len(in.InstanceTypes) > 0 && !containsType(in.InstanceTypes, it.InstanceType) {
			continue
		}
		ok := true
		for _, fl := range in.Filters {
			if !fakeFilterMatches(aws.ToString(fl.Name), fl.Values, it) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	f.fetched += len(out)
	return out, nil
}

func fakeFilterMatches(name string, values []string, it types.InstanceTypeInfo) bool {
	var have []string
	switch name {
	case "instance-type":
		have = []string{string(it.InstanceType)}
	case "processor-info.supported-architecture":
		for _, a := range it.ProcessorInfo.SupportedArchitectures {
			have = append(have, string(a))
		}
	case "vcpu-info.default-vcpus":
		have = []string{strconv.Itoa(int(aws.ToInt32(it.VCpuInfo.DefaultVCpus)))}
	default:
		panic("fakeEC2: unsupported filter " + name)
	}
	for _, v := range values {
		re := regexp.MustCompile("^" + strings.NewReplacer(`\*`, ".*", `\?`, ".").Replace(regexp.QuoteMeta(v)) + "$")
		for _, h := range have {
			if re.MatchString(h) {
				return true
			}
		}
	}
	return false
}

func containsType(list []types.InstanceType, t types.InstanceType) bool {
	for _, x := range list {
		if x == t {
			return true
		}
	}
	return false
}

func filterMap(fs []types.Filter) map[string][]string {
	m := make(map[string][]string, len(fs))
	for _, f := range fs {
		m[aws.ToString(f.Name)] = f.Values
	}
	return m
}

// fixtureCatalog is a slice of a real catalog chosen for edge cases: family
// names that prefix each other (m7i / m7i-flex, c6g / c6gd / c6gn, m8a /
// m8azn, p5 / p5e / p5en), hyphenated families, i386+x86_64 types, and types
// with no DefaultCores (the physical-core estimate path).
func fixtureCatalog() []types.InstanceTypeInfo {
	x86 := []types.ArchitectureType{types.ArchitectureTypeX8664}
	i386 := []types.ArchitectureType{types.ArchitectureTypeI386, types.ArchitectureTypeX8664}
	arm := []types.ArchitectureType{types.ArchitectureTypeArm64}

	type row struct {
		name       string
		vcpus      int32
		cores, tpc int32 // 0 cores = not reported
		memGiB     float64
		arch       []types.ArchitectureType
		nestedVirt bool
	}
	rows := []row{
		{"c8a.large", 2, 2, 1, 4, x86, false},
		{"c8a.4xlarge", 16, 16, 1, 32, x86, false},
		{"c8a.8xlarge", 32, 32, 1, 64, x86, false},
		{"m8a.4xlarge", 16, 16, 1, 64, x86, false},
		{"m8a.metal-24xl", 96, 96, 1, 384, x86, false},
		{"m8azn.4xlarge", 16, 8, 2, 64, x86, false},
		{"r8a.2xlarge", 8, 8, 1, 64, x86, false},
		{"r8a.4xlarge", 16, 16, 1, 128, x86, false},
		{"m7a.4xlarge", 16, 16, 1, 64, x86, false},
		{"m7i.large", 2, 1, 2, 8, x86, false},
		{"m7i.4xlarge", 16, 8, 2, 64, x86, false},
		{"m7i-flex.large", 2, 1, 2, 8, x86, false},
		{"m7i-flex.4xlarge", 16, 8, 2, 64, x86, false},
		{"c7i.medium", 1, 1, 1, 2, x86, false},
		{"c7i.large", 2, 1, 2, 4, x86, false},
		{"c8i.4xlarge", 16, 8, 2, 32, x86, true},
		{"t2.micro", 1, 1, 1, 1, i386, false},
		{"t2.medium", 2, 2, 1, 4, i386, false},
		{"t3.small", 2, 1, 2, 2, x86, false},
		{"c6g.medium", 1, 1, 1, 2, arm, false},
		{"c6g.2xlarge", 8, 8, 1, 16, arm, false},
		{"c6gd.2xlarge", 8, 8, 1, 16, arm, false},
		{"c6gn.4xlarge", 16, 16, 1, 32, arm, false},
		{"m7g.2xlarge", 8, 8, 1, 32, arm, false},
		{"m7gd.4xlarge", 16, 16, 1, 64, arm, false},
		{"r8g.2xlarge", 8, 0, 0, 64, arm, false},
		{"c8g.4xlarge", 16, 0, 1, 32, arm, false},
		{"a1.medium", 1, 1, 1, 2, arm, false},
		{"t4g.small", 2, 2, 1, 2, arm, false},
		{"g5.xlarge", 4, 2, 2, 16, x86, false},
		{"g5g.xlarge", 4, 4, 1, 8, arm, false},
		{"g6.xlarge", 4, 2, 2, 16, x86, false},
		{"g6e.xlarge", 4, 2, 2, 32, x86, false},
		{"p4d.24xlarge", 96, 48, 2, 1152, x86, false},
		{"p4de.24xlarge", 96, 48, 2, 1152, x86, false},
		{"p5.4xlarge", 16, 8, 2, 256, x86, false},
		{"p5.48xlarge", 192, 96, 2, 2048, x86, false},
		{"p5e.48xlarge", 192, 96, 2, 2048, x86, false},
		{"p5en.48xlarge", 192, 96, 2, 2048, x86, false},
		{"p6-b200.48xlarge", 192, 96, 2, 2048, x86, false},
		{"u7i-6tb.112xlarge", 448, 224, 2, 6144, x86, false},
	}

	out := make([]types.InstanceTypeInfo, 0, len(rows))
	for _, r := range rows {
		vc := &types.VCpuInfo{DefaultVCpus: aws.Int32(r.vcpus)}
		if r.cores > 0 {
			vc.DefaultCores = aws.Int32(r.cores)
		}
		if r.tpc > 0 {
			vc.DefaultThreadsPerCore = aws.Int32(r.tpc)
		}
		pi := &types.ProcessorInfo{SupportedArchitectures: r.arch}
		if r.nestedVirt {
			pi.SupportedFeatures = []types.SupportedAdditionalProcessorFeature{types.SupportedAdditionalProcessorFeatureNestedVirtualization}
		}
		out = append(out, types.InstanceTypeInfo{
			InstanceType:  types.InstanceType(r.name),
			VCpuInfo:      vc,
			MemoryInfo:    &types.MemoryInfo{SizeInMiB: aws.Int64(int64(r.memGiB * 1024))},
			ProcessorInfo: pi,
		})
	}
	return out
}

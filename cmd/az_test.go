package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// azFixture: two matching types across three regions, with --min-az-count 4
// already applied by the caller where a test needs it.
func azFixture() []aws.InstanceTypeResult {
	return []aws.InstanceTypeResult{
		{InstanceType: "m7i.large", Region: "us-east-1", AvailableAZs: []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-east-1d", "us-east-1f"}},
		{InstanceType: "m7i.xlarge", Region: "us-east-1", AvailableAZs: []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-east-1e"}},
		{InstanceType: "m7i.large", Region: "us-west-2", AvailableAZs: []string{"us-west-2a", "us-west-2b", "us-west-2c", "us-west-2d"}},
		{InstanceType: "m7i.large", Region: "eu-west-1", AvailableAZs: []string{"eu-west-1a", "eu-west-1b", "eu-west-1c"}},
	}
}

// runAZOutput renders az's output for results as if invoked with args (flags
// only), going through real flag parsing so the test exercises the
// --regions-only binding and not just the helper functions.
func runAZOutput(t *testing.T, args []string, results []aws.InstanceTypeResult) string {
	t.Helper()
	oldFormat, oldColor := outputFormat, noColor
	resetAZFlags(t)
	t.Cleanup(func() {
		outputFormat, noColor = oldFormat, oldColor
		resetAZFlags(t)
	})
	outputFormat, noColor = "table", true
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" {
			outputFormat = args[i+1]
			args = append(args[:i:i], args[i+2:]...)
			break
		}
	}
	if err := azCmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	if minAZCount > 0 {
		results = filterByMinAZCount(results, minAZCount)
	}
	var err error
	out := captureOutput(t, func() { err = printAZResults(results, nil) })
	if err != nil {
		t.Fatalf("printAZResults: %v", err)
	}
	return out
}

// resetAZFlags returns az's local flags to their defaults (pflag keeps parsed
// values across Parse calls).
func resetAZFlags(t *testing.T) {
	t.Helper()
	for _, name := range []string{"regions-only", "min-az-count"} {
		f := azCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("az has no --%s flag", name)
		}
		if err := f.Value.Set(f.DefValue); err != nil {
			t.Fatal(err)
		}
		f.Changed = false
	}
}

// TestAZRegionsOnly asserts the documented intent of `az --regions-only`
// ("Show only regions that meet AZ count requirement", since the first
// release): with --min-az-count N it lists each qualifying region once, not
// each instance type. Before #199 the flag was parsed and ignored, so the
// output was the full per-type table.
func TestAZRegionsOnly(t *testing.T) {
	t.Run("json lists one entry per qualifying region", func(t *testing.T) {
		out := runAZOutput(t, []string{"--min-az-count", "4", "--regions-only", "-o", "json"}, azFixture())
		var got []azRegionSummary
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("not a JSON list of regions: %v\n%s", err, out)
		}
		want := []azRegionSummary{
			{
				Region:            "us-east-1",
				AZCount:           6,
				AvailabilityZones: []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-east-1d", "us-east-1e", "us-east-1f"},
				InstanceTypes:     []string{"m7i.large", "m7i.xlarge"},
			},
			{
				Region:            "us-west-2",
				AZCount:           4,
				AvailabilityZones: []string{"us-west-2a", "us-west-2b", "us-west-2c", "us-west-2d"},
				InstanceTypes:     []string{"m7i.large"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("--regions-only JSON =\n%+v\nwant (eu-west-1 has only 3 AZs, so it is excluded)\n%+v", got, want)
		}
		// The per-type shape has instance_type keys; the region list must not.
		if strings.Contains(out, `"instance_type"`) {
			t.Errorf("--regions-only JSON still has per-instance-type entries:\n%s", out)
		}
	})

	t.Run("table has one row per region", func(t *testing.T) {
		out := runAZOutput(t, []string{"--min-az-count", "4", "--regions-only"}, azFixture())
		var rows []string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "us-") || strings.HasPrefix(line, "eu-") {
				rows = append(rows, strings.Fields(line)[0])
			}
		}
		if want := []string{"us-east-1", "us-west-2"}; !reflect.DeepEqual(rows, want) {
			t.Errorf("--regions-only table rows = %v, want %v\n%s", rows, want, out)
		}
		if strings.Contains(out, "AZ Availability Summary") {
			t.Errorf("--regions-only table still prints the per-type summary:\n%s", out)
		}
	})

	t.Run("the flag changes the output", func(t *testing.T) {
		for _, format := range []string{"table", "json", "yaml", "csv"} {
			without := runAZOutput(t, []string{"--min-az-count", "4", "-o", format}, azFixture())
			with := runAZOutput(t, []string{"--min-az-count", "4", "--regions-only", "-o", format}, azFixture())
			if with == without {
				t.Errorf("-o %s: --regions-only produced the same output as without it:\n%s", format, with)
			}
		}
	})
}

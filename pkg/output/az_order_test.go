package output

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// TestEveryFormatListsAZsSorted is the output-side gate for #217: whatever
// order a result's AZs arrive in, table, JSON, YAML and CSV all list them
// sorted, and the caller's slice is left alone.
func TestEveryFormatListsAZsSorted(t *testing.T) {
	shuffled := []string{"us-west-2d", "us-west-2-lax-1a", "us-west-2b", "us-west-2a", "us-west-2c"}
	results := []aws.InstanceTypeResult{{
		InstanceType: "m8a.4xlarge", Region: "us-west-2", VCPUs: 16, MemoryMiB: 65536,
		Architecture: "x86_64", AvailableAZs: shuffled,
	}}
	orig := append([]string(nil), shuffled...)
	p := NewPrinter(false)

	cases := []struct {
		format string
		print  func() error
		want   string
	}{
		{"table", func() error { return p.PrintTable(results, true, false) }, "us-west-2a, us-west-2b, us-west-2c"},
		{"json", func() error { return p.PrintJSON(results) }, `"us-west-2a", "us-west-2b", "us-west-2c", "us-west-2d", "us-west-2-lax-1a"`},
		{"yaml", func() error { return p.PrintYAML(results) }, "- us-west-2a - us-west-2b - us-west-2c - us-west-2d - us-west-2-lax-1a"},
		{"csv", func() error { return p.PrintCSV(results) }, "us-west-2a;us-west-2b;us-west-2c;us-west-2d;us-west-2-lax-1a"},
	}
	for _, tc := range cases {
		var err error
		out := captureStdout(t, func() { err = tc.print() })
		if err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		// Collapse whitespace so multi-line JSON/YAML lists compare as one line.
		if !strings.Contains(strings.Join(strings.Fields(out), " "), tc.want) {
			t.Errorf("%s output does not list AZs sorted (want %q):\n%s", tc.format, tc.want, out)
		}
	}
	if !reflect.DeepEqual(results[0].AvailableAZs, orig) {
		t.Errorf("printing modified the caller's AZ slice: %v, want %v", results[0].AvailableAZs, orig)
	}
}

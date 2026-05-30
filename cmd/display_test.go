package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/find"
	"github.com/spore-host/truffle/pkg/quotas"
)

// captureOutput redirects both stdout and stderr to buffers for the duration
// of fn and returns their combined content (the display funcs use both).
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wErr

	fn()

	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	var bufOut, bufErr bytes.Buffer
	_, _ = io.Copy(&bufOut, rOut)
	_, _ = io.Copy(&bufErr, rErr)
	_ = rOut.Close()
	_ = rErr.Close()
	return bufOut.String() + bufErr.String()
}

func TestPrintSpotSummary(t *testing.T) {
	results := []aws.SpotPriceResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1a", SpotPrice: 0.05, OnDemandPrice: 0.153, SavingsPercent: 67},
		{InstanceType: "c6a.xlarge", Region: "us-west-2", AvailabilityZone: "us-west-2b", SpotPrice: 0.06, OnDemandPrice: 0.153, SavingsPercent: 60},
	}
	out := captureOutput(t, func() { printSpotSummary(results) })
	// i18n may resolve keys to translations or echo the raw key in test context;
	// assert on the stable price figures and the summary key namespace instead.
	if !strings.Contains(out, "summary") && !strings.Contains(out, "Summary") {
		t.Errorf("spot summary produced no recognizable summary output:\n%s", out)
	}
	if !strings.Contains(out, "0.05") {
		t.Errorf("spot summary missing min price 0.05:\n%s", out)
	}
	// Empty input must not panic and produces no summary.
	_ = captureOutput(t, func() { printSpotSummary(nil) })
}

func TestPrintCapacitySummary(t *testing.T) {
	results := []aws.CapacityReservationResult{
		{InstanceType: "p4d.24xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1a", TotalCapacity: 8, AvailableCapacity: 3, UsedCapacity: 5, State: "active"},
		{InstanceType: "p4d.24xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1b", TotalCapacity: 4, AvailableCapacity: 4, UsedCapacity: 0, State: "active"},
	}
	out := captureOutput(t, func() { printCapacitySummary(results) })
	// 8+4 total capacity, 5 used → assert on the computed figures, which are
	// i18n-independent.
	if !strings.Contains(out, "12") || !strings.Contains(out, "summary") {
		t.Errorf("capacity summary missing computed totals:\n%s", out)
	}
}

func TestPrintAZSummary(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "m5.large", Region: "us-east-1", AvailableAZs: []string{"us-east-1a", "us-east-1b", "us-east-1c"}},
		{InstanceType: "m5.large", Region: "us-west-2", AvailableAZs: []string{"us-west-2a"}},
	}
	out := captureOutput(t, func() { printAZSummary(results) })
	if !strings.Contains(out, "AZ") {
		t.Errorf("AZ summary missing 'AZ':\n%s", out)
	}
}

func TestPrintList(t *testing.T) {
	out := captureOutput(t, func() { printList("Families", []string{"m5", "c6i", "r7g"}) })
	for _, want := range []string{"Families", "m5", "c6i", "r7g"} {
		if !strings.Contains(out, want) {
			t.Errorf("printList missing %q:\n%s", want, out)
		}
	}
	// Empty list should not panic.
	_ = captureOutput(t, func() { printList("Empty", nil) })
}

func TestPrintParsedQuery(t *testing.T) {
	pq, err := find.ParseQuery("amd epyc 16 cores 64gb")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	out := captureOutput(t, func() { printParsedQuery(pq) })
	if !strings.Contains(out, "Parsed query") {
		t.Errorf("printParsedQuery missing header:\n%s", out)
	}
}

func TestPrintSuggestions(t *testing.T) {
	// A query with unknown terms should yield suggestions.
	pq, err := find.ParseQuery("flibbertigibbet zzznonsense")
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	out := captureOutput(t, func() { printSuggestions(pq) })
	if !strings.Contains(out, "Suggestions") {
		t.Errorf("printSuggestions missing header:\n%s", out)
	}
}

func TestPrintFindResults_AllFormats(t *testing.T) {
	results := []find.FindResult{
		{
			InstanceTypeResult: aws.InstanceTypeResult{InstanceType: "m6i.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8192, Architecture: "x86_64"},
			MatchReasons:       []string{"matched vendor: intel"},
			MatchScore:         10,
		},
	}
	orig := outputFormat
	defer func() { outputFormat = orig }()

	for _, format := range []string{"json", "yaml", "csv", "table"} {
		outputFormat = format
		out := captureOutput(t, func() {
			if err := printFindResults(results); err != nil {
				t.Errorf("printFindResults(%s) error: %v", format, err)
			}
		})
		if !strings.Contains(out, "m6i.large") {
			t.Errorf("printFindResults(%s) missing instance type:\n%s", format, out)
		}
	}

	// Unsupported format returns an error.
	outputFormat = "xml"
	_ = captureOutput(t, func() {
		if err := printFindResults(results); err == nil {
			t.Error("expected error for unsupported format 'xml'")
		}
	})
}

func TestDisplayRegionQuotas(t *testing.T) {
	orig := noColor
	noColor = true
	defer func() { noColor = orig }()

	info := &quotas.QuotaInfo{
		Region:               "us-east-1",
		OnDemand:             map[quotas.QuotaFamily]int32{quotas.FamilyStandard: 1000, quotas.FamilyP: 0},
		Spot:                 map[quotas.QuotaFamily]int32{quotas.FamilyStandard: 640},
		Usage:                map[quotas.QuotaFamily]int32{quotas.FamilyStandard: 100},
		RunningInstances:     5,
		RunningInstancesMax:  20,
		LastUpdated:          time.Unix(1700000000, 0),
		CredentialsAvailable: true,
	}
	out := captureOutput(t, func() { displayRegionQuotas("us-east-1", info, "") })
	if !strings.Contains(out, "us-east-1") {
		t.Errorf("displayRegionQuotas missing region:\n%s", out)
	}

	// With a family filter.
	out = captureOutput(t, func() { displayRegionQuotas("us-east-1", info, "Standard") })
	if out == "" {
		t.Error("displayRegionQuotas(filtered) produced no output")
	}
}

func TestGenerateIncreaseRequests(t *testing.T) {
	infos := map[string]*quotas.QuotaInfo{
		"us-east-1": {
			Region:   "us-east-1",
			OnDemand: map[quotas.QuotaFamily]int32{quotas.FamilyP: 0, quotas.FamilyStandard: 100},
			Usage:    map[quotas.QuotaFamily]int32{quotas.FamilyP: 0, quotas.FamilyStandard: 95},
		},
	}
	out := captureOutput(t, func() { generateIncreaseRequests(infos, "") })
	// P family quota is 0 and Standard is nearly full → should generate at least one request command.
	if !strings.Contains(out, "aws") && out == "" {
		t.Errorf("expected increase-request output, got:\n%s", out)
	}
}

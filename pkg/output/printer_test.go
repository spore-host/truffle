package output

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fatih/color"
	"github.com/spore-host/truffle/pkg/aws"
)

// captureStdout redirects os.Stdout to a buffer for the duration of fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

func sampleInstances() []aws.InstanceTypeResult {
	return []aws.InstanceTypeResult{
		{InstanceType: "m7g.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8192, Architecture: "arm64"},
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64"},
		{InstanceType: "m7g.large", Region: "eu-west-1", VCPUs: 2, MemoryMiB: 8192, Architecture: "arm64"},
	}
}

func sampleSpotPrices() []aws.SpotPriceResult {
	return []aws.SpotPriceResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1a", SpotPrice: 0.0512},
		{InstanceType: "m7g.large", Region: "eu-west-1", AvailabilityZone: "eu-west-1b", SpotPrice: 0.0314},
	}
}

func sampleCapacity() []aws.CapacityReservationResult {
	return []aws.CapacityReservationResult{
		{ReservationID: "cr-0abc123def456", Region: "us-east-1", InstanceType: "m7g.large", AvailableCapacity: 10, TotalCapacity: 10},
	}
}

// --- JSON ---

func TestPrintJSON_ValidOutput(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintJSON(sampleInstances()); err != nil {
			t.Errorf("PrintJSON returned error: %v", err)
		}
	})
	var results []aws.InstanceTypeResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	if results[0].InstanceType != "m7g.large" {
		t.Errorf("unexpected first instance type: %s", results[0].InstanceType)
	}
}

func TestPrintSpotJSON_ValidOutput(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintSpotJSON(sampleSpotPrices()); err != nil {
			t.Errorf("PrintSpotJSON returned error: %v", err)
		}
	})
	var results []aws.SpotPriceResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestPrintCapacityJSON_ValidOutput(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintCapacityJSON(sampleCapacity()); err != nil {
			t.Errorf("PrintCapacityJSON returned error: %v", err)
		}
	})
	if !strings.Contains(out, "cr-0abc123") {
		t.Errorf("output missing reservation ID: %s", out)
	}
}

// --- YAML ---

func TestPrintYAML_ContainsExpectedFields(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintYAML(sampleInstances()); err != nil {
			t.Errorf("PrintYAML returned error: %v", err)
		}
	})
	for _, want := range []string{"m7g.large", "c6a.xlarge", "us-east-1", "arm64"} {
		if !strings.Contains(out, want) {
			t.Errorf("YAML output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintSpotYAML_ContainsExpectedFields(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintSpotYAML(sampleSpotPrices()); err != nil {
			t.Errorf("PrintSpotYAML returned error: %v", err)
		}
	})
	if !strings.Contains(out, "c6a.xlarge") {
		t.Errorf("YAML output missing instance type: %s", out)
	}
}

func TestPrintCapacityYAML_ContainsExpectedFields(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintCapacityYAML(sampleCapacity()); err != nil {
			t.Errorf("PrintCapacityYAML returned error: %v", err)
		}
	})
	if !strings.Contains(out, "m7g.large") {
		t.Errorf("YAML output missing instance type: %s", out)
	}
}

// --- CSV ---

func TestPrintCSV_HasHeaderAndRows(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintCSV(sampleInstances()); err != nil {
			t.Errorf("PrintCSV returned error: %v", err)
		}
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + rows, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "instance_type") && !strings.Contains(lines[0], "INSTANCE") {
		t.Errorf("first line doesn't look like a header: %s", lines[0])
	}
}

func TestPrintSpotCSV_HasRows(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintSpotCSV(sampleSpotPrices()); err != nil {
			t.Errorf("PrintSpotCSV returned error: %v", err)
		}
	})
	if !strings.Contains(out, "c6a.xlarge") {
		t.Errorf("CSV output missing instance type: %s", out)
	}
}

func TestPrintCapacityCSV_HasRows(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintCapacityCSV(sampleCapacity()); err != nil {
			t.Errorf("PrintCapacityCSV returned error: %v", err)
		}
	})
	if !strings.Contains(out, "m7g.large") {
		t.Errorf("CSV output missing instance type: %s", out)
	}
}

// --- helpers ---

func TestGroupByInstanceType(t *testing.T) {
	grouped := groupByInstanceType(sampleInstances())
	if len(grouped["m7g.large"]) != 2 {
		t.Errorf("expected 2 m7g.large entries, got %d", len(grouped["m7g.large"]))
	}
	if len(grouped["c6a.xlarge"]) != 1 {
		t.Errorf("expected 1 c6a.xlarge entry, got %d", len(grouped["c6a.xlarge"]))
	}
	if grouped := groupByInstanceType(nil); len(grouped) != 0 {
		t.Errorf("expected empty map for nil input")
	}
}

func TestCountUniqueRegions(t *testing.T) {
	if n := countUniqueRegions(sampleInstances()); n != 2 {
		t.Errorf("expected 2 unique regions, got %d", n)
	}
	if n := countUniqueRegions(nil); n != 0 {
		t.Errorf("expected 0 for nil input, got %d", n)
	}
}

func TestShortenReservationID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cr-0abc123def456789", "cr-0abc123..."},
		{"short", "short"},
		{"exactly10c", "exactly10c"},
		{"", ""},
	}
	for _, tt := range tests {
		got := shortenReservationID(tt.input)
		if got != tt.want {
			t.Errorf("shortenReservationID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripAnsiCodes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello", "hello"},
		{"empty", "", ""},
		{"single color", "\x1b[32mgreen\x1b[0m", "green"},
		{"bold cyan", "\x1b[36;1mHEADER\x1b[0m", "HEADER"},
		{"checkmark with color", "us-east-1 \x1b[32m✓\x1b[0m", "us-east-1 ✓"},
		{"only escape", "\x1b[0m", ""},
		{"multibyte preserved", "café", "café"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripAnsiCodes(tt.input); got != tt.want {
				t.Errorf("stripAnsiCodes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTruncateWithEllipsis(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"under limit", "short", 40, "short"},
		{"exactly at limit", "1234567890", 10, "1234567890"},
		{"breaks on comma boundary", "us-east-1a, us-east-1b, us-east-1c, us-east-1d", 30, "us-east-1a, us-east-1b..."},
		{"hard truncate no boundary", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 20, "aaaaaaaaaaaaaaaaa..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateWithEllipsis(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateWithEllipsis(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			// Result must never exceed maxLen runes.
			if n := len([]rune(got)); n > tt.maxLen && n > len([]rune(tt.input)) {
				t.Errorf("result %q has %d runes, exceeds maxLen %d", got, n, tt.maxLen)
			}
		})
	}
}

// --- table rendering ---

// boxChars are the border-drawing runes the renderer emits; their presence
// confirms a table was actually drawn.
const boxChars = "┌┐└┘├┤┬┴┼─│"

func hasTableBorders(s string) bool {
	for _, r := range "┌─┐│└┘" {
		if !strings.ContainsRune(s, r) {
			return false
		}
	}
	return true
}

func TestPrintTable_BasicStructure(t *testing.T) {
	p := NewPrinter(false)
	out := captureStdout(t, func() {
		if err := p.PrintTable(sampleInstances(), false, false); err != nil {
			t.Errorf("PrintTable returned error: %v", err)
		}
	})
	if !hasTableBorders(out) {
		t.Fatalf("output missing table borders:\n%s", out)
	}
	for _, want := range []string{"m7g.large", "c6a.xlarge", "us-east-1", "arm64", "x86_64"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	// m7g.large appears in two regions but should be deduped to distinct rows;
	// the instance type label is blanked on continuation rows.
	if strings.Count(out, "m7g.large") != 1 {
		t.Errorf("expected m7g.large label once (grouped), got %d:\n%s", strings.Count(out, "m7g.large"), out)
	}
}

func TestPrintTable_WithAZsAndPrice(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{
			InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192,
			Architecture: "x86_64", OnDemandPrice: 0.1530,
			AvailableAZs: []string{"us-east-1a", "us-east-1b", "us-east-1c"},
		},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, true, true)
	})
	if !strings.Contains(out, "$0.1530") {
		t.Errorf("price column missing:\n%s", out)
	}
	if !strings.Contains(out, "us-east-1a") {
		t.Errorf("AZ column missing:\n%s", out)
	}
}

func TestPrintTable_PriceNA(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, true)
	})
	if !strings.Contains(out, "N/A") {
		t.Errorf("expected N/A for zero on-demand price:\n%s", out)
	}
}

func TestPrintTable_GPUColumns(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{
			InstanceType: "g5.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 16384,
			Architecture: "x86_64", GPUs: 1, GPUModel: "A10G", GPUManufacturer: "nvidia",
			GPUMemoryMiB: 24576,
		},
		// non-GPU row in the same table renders placeholder dashes
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	for _, want := range []string{"GPUs", "GPU Model", "nvidia A10G", "24"} {
		if !strings.Contains(out, want) {
			t.Errorf("GPU table missing %q:\n%s", want, out)
		}
	}
}

// TestPrintTable_PerGPUVRAM_WholeGPU is the #116 fix's happy path: a whole-GPU
// type shows both the existing total VRAM and the new per-GPU figure.
func TestPrintTable_PerGPUVRAM_WholeGPU(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{
			InstanceType: "g5.12xlarge", Region: "us-east-1", VCPUs: 48, MemoryMiB: 196608,
			Architecture: "x86_64", GPUs: 4, GPUModel: "A10G", GPUManufacturer: "nvidia",
			GPUMemoryMiB: 98304, GPUMemoryPerMiB: 24576,
		},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	if !strings.Contains(out, "96 (24/gpu)") {
		t.Errorf("expected total+per-GPU VRAM figure %q:\n%s", "96 (24/gpu)", out)
	}
}

// TestPrintTable_PerGPUVRAM_FractionalGPU is the regression test for #116:
// fractional-GPU types report GPUs==0 (AWS represents the slice via
// GpuPartitionSize, not a whole-GPU count). Before the fix, these rows got NO
// GPU columns at all because hasGPU/the per-row gate only checked GPUs>0.
func TestPrintTable_PerGPUVRAM_FractionalGPU(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{
			InstanceType: "g6f.large", Region: "us-east-1", VCPUs: 8, MemoryMiB: 16384,
			Architecture: "x86_64", GPUs: 0, GPUModel: "L4", GPUManufacturer: "nvidia",
			GPUMemoryMiB: 2861, GPUMemoryPerMiB: 2861, GPUPartitionSize: 0.125,
		},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	if !strings.Contains(out, "GPU Model") {
		t.Fatalf("fractional-GPU row lost its GPU columns entirely (#116 regression):\n%s", out)
	}
	if !strings.Contains(out, "nvidia L4") {
		t.Errorf("expected GPU model for fractional-GPU row:\n%s", out)
	}
	if !strings.Contains(out, "0.125") {
		t.Errorf("expected fractional GPU-count indicator instead of a misleading 0:\n%s", out)
	}
}

// TestPrintTable_ShowRealCores_Hyperthreaded covers a 2-threads-per-core type
// (most x86), where vCPUs != physical cores.
func TestPrintTable_ShowRealCores_Hyperthreaded(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6i.24xlarge", Region: "us-east-1", VCPUs: 96, PhysicalCores: 48, ThreadsPerCore: 2, MemoryMiB: 196608, Architecture: "x86_64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowRealCores: true})
	})
	if !strings.Contains(out, "96/48") {
		t.Errorf("expected vCPU/CPU format 96/48:\n%s", out)
	}
	if !strings.Contains(out, "vCPU/physical-core") {
		t.Errorf("expected real-cores footer note:\n%s", out)
	}
}

// TestPrintTable_ShowRealCores_OneToOne covers a 1-thread-per-core type
// (Graviton), where vCPUs == physical cores.
func TestPrintTable_ShowRealCores_OneToOne(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "m7g.16xlarge", Region: "us-east-1", VCPUs: 64, PhysicalCores: 64, ThreadsPerCore: 1, MemoryMiB: 262144, Architecture: "arm64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowRealCores: true})
	})
	if !strings.Contains(out, "64/64") {
		t.Errorf("expected vCPU/CPU format 64/64:\n%s", out)
	}
}

// TestPrintTable_ShowRealCores_DefaultOff confirms the flag is opt-in: with
// the zero-value TableOptions, the vCPU cell stays a plain count.
func TestPrintTable_ShowRealCores_DefaultOff(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6i.24xlarge", Region: "us-east-1", VCPUs: 96, PhysicalCores: 48, MemoryMiB: 196608, Architecture: "x86_64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	if strings.Contains(out, "96/48") {
		t.Errorf("vCPU/CPU format should not appear without --show-real-cores:\n%s", out)
	}
}

func TestPrintTable_ShowMemPerCPU(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6i.24xlarge", Region: "us-east-1", VCPUs: 96, PhysicalCores: 48, MemoryMiB: 196608, Architecture: "x86_64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowMemPerCPU: true})
	})
	if !strings.Contains(out, "192.0 (4.0/core)") {
		t.Errorf("expected total+per-physical-core memory figure:\n%s", out)
	}
	if !strings.Contains(out, "per-physical-core") {
		t.Errorf("expected mem-per-cpu footer note:\n%s", out)
	}
}

func TestPrintTable_ShowMemPerCPU_DefaultOff(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6i.24xlarge", Region: "us-east-1", VCPUs: 96, PhysicalCores: 48, MemoryMiB: 196608, Architecture: "x86_64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	if strings.Contains(out, "/core") {
		t.Errorf("mem-per-cpu format should not appear without --show-mem-per-cpu:\n%s", out)
	}
}

func TestPrintTable_PriceUnit_DefaultIsHour(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64", OnDemandPrice: 0.1530},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowPrice: true})
	})
	if !strings.Contains(out, "$/hr") || !strings.Contains(out, "$0.1530") {
		t.Errorf("expected unchanged default hourly format:\n%s", out)
	}
}

func TestPrintTable_PriceUnit_Minute(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64", OnDemandPrice: 0.1530},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowPrice: true, PriceUnit: PriceUnitMinute})
	})
	if !strings.Contains(out, "$/min") {
		t.Errorf("expected $/min header:\n%s", out)
	}
	if !strings.Contains(out, "$0.002550") {
		t.Errorf("expected per-minute price conversion:\n%s", out)
	}
}

func TestPrintTable_PriceUnit_Second(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64", OnDemandPrice: 0.1530},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowPrice: true, PriceUnit: PriceUnitSecond})
	})
	if !strings.Contains(out, "$/sec") {
		t.Errorf("expected $/sec header:\n%s", out)
	}
	if !strings.Contains(out, "$0.00004250") {
		t.Errorf("expected per-second price conversion:\n%s", out)
	}
}

func TestPrintTable_SpawnSupportedFooter(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 8192, Architecture: "x86_64", SpawnSupported: true},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	if !strings.Contains(out, "✓ = spawn-supported region") {
		t.Errorf("expected spawn-supported footer note:\n%s", out)
	}
	if !strings.Contains(out, "✓ us-east-1") {
		t.Errorf("expected spawn-support marker before region (#135):\n%s", out)
	}
}

// TestPrintTable_RegionColumnAlignsAcrossSupportAndUnsupported guards against
// a supported row's "✓ " marker shifting its region name two characters to
// the right of an unsupported row's region name, which reads as ragged
// rather than tabular. An unsupported row gets an equal-width blank pad
// instead of nothing, so every region name starts in the same column.
func TestPrintTable_RegionColumnAlignsAcrossSupportAndUnsupported(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "g6e.24xlarge", Region: "ap-northeast-1", VCPUs: 96, MemoryMiB: 768 * 1024, Architecture: "x86_64", SpawnSupported: true},
		{InstanceType: "g6e.24xlarge", Region: "ap-northeast-2", VCPUs: 96, MemoryMiB: 768 * 1024, Architecture: "x86_64", SpawnSupported: false},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	lines := strings.Split(out, "\n")
	var supportedLine, unsupportedLine string
	for _, l := range lines {
		if strings.Contains(l, "ap-northeast-1") {
			supportedLine = l
		}
		if strings.Contains(l, "ap-northeast-2") {
			unsupportedLine = l
		}
	}
	if supportedLine == "" || unsupportedLine == "" {
		t.Fatalf("expected both region rows in output:\n%s", out)
	}
	// Rune columns, not byte offsets — "✓" is a multi-byte UTF-8 character,
	// so strings.Index's byte offset would differ from the visual column
	// even when the rows are correctly aligned.
	supportedCol := utf8.RuneCountInString(supportedLine[:strings.Index(supportedLine, "ap-northeast-1")])
	unsupportedCol := utf8.RuneCountInString(unsupportedLine[:strings.Index(unsupportedLine, "ap-northeast-2")])
	if supportedCol != unsupportedCol {
		t.Errorf("region names not aligned: supported row starts region name at visual col %d (%q), unsupported at col %d (%q)",
			supportedCol, supportedLine, unsupportedCol, unsupportedLine)
	}
}

// TestPrintTable_NumericColumnsRightAligned guards the right-justification
// of numeric columns (vCPUs, memory, GPUs, VRAM, price) — left-aligned
// numbers of differing width read as ragged rather than tabular.
func TestPrintTable_NumericColumnsRightAligned(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "g6e.2xlarge", Region: "us-east-1", VCPUs: 8, MemoryMiB: 64 * 1024, Architecture: "x86_64", GPUs: 1, GPUModel: "L40S", GPUManufacturer: "NVIDIA", GPUMemoryMiB: 45 * 1024, OnDemandPrice: 3.25},
		{InstanceType: "g6e.24xlarge", Region: "us-east-1", VCPUs: 96, MemoryMiB: 768 * 1024, Architecture: "x86_64", GPUs: 4, GPUModel: "L40S", GPUManufacturer: "NVIDIA", GPUMemoryMiB: 179 * 1024, OnDemandPrice: 21.85},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowPrice: true})
	})
	lines := strings.Split(out, "\n")
	var shortRow, longRow string
	for _, l := range lines {
		if strings.Contains(l, "g6e.2xlarge ") {
			shortRow = l
		}
		if strings.Contains(l, "g6e.24xlarge") {
			longRow = l
		}
	}
	if shortRow == "" || longRow == "" {
		t.Fatalf("expected both instance rows in output:\n%s", out)
	}
	// "8" (1-digit vCPU) and "96" (2-digit vCPU) must end (be right-aligned)
	// at the same column, not start at the same column.
	shortEnd := strings.Index(shortRow, "8") + 1
	longEnd := strings.Index(longRow, "96") + 2
	if shortEnd != longEnd {
		t.Errorf("vCPU column not right-aligned: %q vs %q", shortRow, longRow)
	}
}

// TestPrintTable_InstanceTypeGroupsSortedLargestFirst guards against Go's
// randomized map-iteration order: instance-type groups must render
// largest-to-smallest vCPU (then Memory) rather than in whatever order
// groupByInstanceType's map happens to yield.
func TestPrintTable_InstanceTypeGroupsSortedLargestFirst(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "m7g.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8 * 1024, Architecture: "arm64"},
		{InstanceType: "m7g.4xlarge", Region: "us-east-1", VCPUs: 16, MemoryMiB: 64 * 1024, Architecture: "arm64"},
		{InstanceType: "m7g.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 16 * 1024, Architecture: "arm64"},
		{InstanceType: "m7g.2xlarge", Region: "us-east-1", VCPUs: 8, MemoryMiB: 32 * 1024, Architecture: "arm64"},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false)
	})
	want := []string{"m7g.4xlarge", "m7g.2xlarge", "m7g.xlarge", "m7g.large"}
	var gotOrder []string
	for _, l := range strings.Split(out, "\n") {
		for _, it := range want {
			if strings.Contains(l, it) {
				gotOrder = append(gotOrder, it)
				break
			}
		}
	}
	if len(gotOrder) != len(want) {
		t.Fatalf("expected %d instance-type rows, got %d:\n%s", len(want), len(gotOrder), out)
	}
	for i, it := range want {
		if gotOrder[i] != it {
			t.Errorf("row %d = %q, want %q (order: %v)", i, gotOrder[i], it, gotOrder)
		}
	}
}

// TestPrintTable_WithinGroupSortedByPriceDescending guards the within-group
// ordering: once instance-type groups are sorted largest-first, the rows
// inside a single group (one per region/AZ) must be highest-price first.
func TestPrintTable_WithinGroupSortedByPriceDescending(t *testing.T) {
	results := []aws.InstanceTypeResult{
		{InstanceType: "c7g.large", Region: "us-west-2", VCPUs: 2, MemoryMiB: 4 * 1024, Architecture: "arm64", OnDemandPrice: 0.05},
		{InstanceType: "c7g.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 4 * 1024, Architecture: "arm64", OnDemandPrice: 0.09},
		{InstanceType: "c7g.large", Region: "eu-west-1", VCPUs: 2, MemoryMiB: 4 * 1024, Architecture: "arm64", OnDemandPrice: 0.07},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(results, TableOptions{ShowPrice: true})
	})
	want := []string{"us-east-1", "eu-west-1", "us-west-2"} // 0.09, 0.07, 0.05
	var gotOrder []string
	for _, l := range strings.Split(out, "\n") {
		for _, region := range want {
			if strings.Contains(l, region) {
				gotOrder = append(gotOrder, region)
				break
			}
		}
	}
	if len(gotOrder) != len(want) {
		t.Fatalf("expected %d region rows, got %d:\n%s", len(want), len(gotOrder), out)
	}
	for i, region := range want {
		if gotOrder[i] != region {
			t.Errorf("row %d = %q, want %q (order: %v)", i, gotOrder[i], region, gotOrder)
		}
	}
}

func TestPrintTable_SageMakerSpotEligibleAndFooter(t *testing.T) {
	q := 4.0
	results := []aws.InstanceTypeResult{
		{
			InstanceType: "ml.g5.2xlarge", Region: "us-east-1", VCPUs: 8, MemoryMiB: 32768,
			Architecture: "x86_64", OnDemandPrice: 1.515, Service: "sagemaker",
			ManagedSpotEligible: true, TrainingJobQuota: &q,
		},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithQuota(results, false, true)
	})
	// Spot-Eligible column + marker.
	if !strings.Contains(out, "Spot-Eligible") {
		t.Errorf("expected Spot-Eligible column:\n%s", out)
	}
	// Train Quota column shows the limit.
	if !strings.Contains(out, "Train Quota") {
		t.Errorf("expected Train Quota column:\n%s", out)
	}
	// SageMaker footer + managed-spot note.
	if !strings.Contains(out, "SageMaker ml.* types") {
		t.Errorf("expected SageMaker footer:\n%s", out)
	}
	if !strings.Contains(out, "managed spot training") {
		t.Errorf("expected managed-spot footer note:\n%s", out)
	}
}

func TestPrintTable_NoQuotaColumnWithoutFlag(t *testing.T) {
	q := 4.0
	results := []aws.InstanceTypeResult{
		{
			InstanceType: "ml.g5.2xlarge", Region: "us-east-1", VCPUs: 8, MemoryMiB: 32768,
			Architecture: "x86_64", Service: "sagemaker", TrainingJobQuota: &q,
		},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(results, false, false) // no quota
	})
	if strings.Contains(out, "Train Quota") {
		t.Errorf("Train Quota column should be absent without --show-quota:\n%s", out)
	}
}

func TestPrintTable_ColorMode(t *testing.T) {
	// fatih/color disables ANSI when stdout is not a TTY (our capture pipe).
	// Force it on so we exercise the colored render path, then restore.
	prev := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = prev }()

	// With color on, the rendered width must still align: the renderer strips
	// ANSI codes before measuring, so borders should be intact.
	out := captureStdout(t, func() {
		_ = NewPrinter(true).PrintTable(sampleInstances(), false, false)
	})
	if !hasTableBorders(out) {
		t.Fatalf("colored table missing borders:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI color codes in colored output")
	}
}

func TestPrintSpotTable_Structure(t *testing.T) {
	prices := []aws.SpotPriceResult{
		{InstanceType: "c6a.xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1a", SpotPrice: 0.0512, OnDemandPrice: 0.1530, SavingsPercent: 66.5},
		{InstanceType: "c6a.xlarge", Region: "us-east-1", AvailabilityZone: "us-east-1b", SpotPrice: 0.0534, OnDemandPrice: 0.1530, SavingsPercent: 65.1},
	}
	out := captureStdout(t, func() {
		if err := NewPrinter(false).PrintSpotTable(prices); err != nil {
			t.Errorf("PrintSpotTable error: %v", err)
		}
	})
	if !hasTableBorders(out) {
		t.Fatalf("spot table missing borders:\n%s", out)
	}
	for _, want := range []string{"c6a.xlarge", "$0.0512", "$0.1530", "66%"} {
		if !strings.Contains(out, want) {
			t.Errorf("spot table missing %q:\n%s", want, out)
		}
	}
}

func TestPrintSpotTable_NoSavingsData(t *testing.T) {
	// Without ShowSavings, OnDemandPrice is 0 → On-Demand/Savings show N/A.
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintSpotTable(sampleSpotPrices())
	})
	if !strings.Contains(out, "N/A") {
		t.Errorf("expected N/A for missing on-demand price:\n%s", out)
	}
}

func TestPrintCapacityTable_Structure(t *testing.T) {
	results := []aws.CapacityReservationResult{
		{ReservationID: "cr-0abc123def456789", Region: "us-east-1", AvailabilityZone: "us-east-1a", InstanceType: "p4d.24xlarge", TotalCapacity: 8, AvailableCapacity: 3, UsedCapacity: 5, State: "active"},
	}
	out := captureStdout(t, func() {
		if err := NewPrinter(false).PrintCapacityTable(results); err != nil {
			t.Errorf("PrintCapacityTable error: %v", err)
		}
	})
	if !hasTableBorders(out) {
		t.Fatalf("capacity table missing borders:\n%s", out)
	}
	for _, want := range []string{"p4d.24xlarge", "active", "cr-0abc123...", "5 (62%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("capacity table missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTable_Empty(t *testing.T) {
	// Empty input should still render header borders without panicking.
	out := captureStdout(t, func() {
		if err := NewPrinter(false).PrintTable(nil, false, false); err != nil {
			t.Errorf("PrintTable(nil) error: %v", err)
		}
	})
	if !strings.ContainsAny(out, boxChars) {
		t.Errorf("expected at least header borders for empty table:\n%s", out)
	}
}

func TestFormatLocalWindow(t *testing.T) {
	// Pin the local zone to UTC so the assertions are deterministic regardless of
	// where the test runs.
	orig := time.Local
	time.Local = time.UTC
	defer func() { time.Local = orig }()

	cases := []struct {
		name, start, end, want string
	}{
		{
			name:  "same local day drops the end date",
			start: "2026-06-18T04:30:00Z",
			end:   "2026-06-18T11:30:00Z",
			want:  "Jun 18 04:30 → 11:30 UTC",
		},
		{
			name:  "multi-day keeps the end date",
			start: "2026-06-18T11:30:00Z",
			end:   "2026-06-19T11:30:00Z",
			want:  "Jun 18 11:30 → Jun 19 11:30 UTC",
		},
		{
			name:  "bad start falls back to raw",
			start: "not-a-time",
			end:   "2026-06-19T11:30:00Z",
			want:  "not-a-time",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatLocalWindow(c.start, c.end); got != c.want {
				t.Errorf("formatLocalWindow(%q,%q) = %q, want %q", c.start, c.end, got, c.want)
			}
		})
	}
}

package output

import (
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// TestPrintTable_AcceleratorNominal checks the documented per-device sizes
// in the table (#242): trn2.48xlarge's 16 Trainium2 chips are 96 GiB each,
// 1536 GiB in all (AWS: 1.5 TB), labelled per chip, not EC2's 512 GiB per
// device; a B200 is 179 GiB and a B300 268 GiB, AWS's per-instance totals
// over 8 GPUs, not 179.1 and 268.6. They are binary sizes, so they are
// labelled GiB, not GB (#244: a B200 read "179 GB"). The AccelMemoryMiB is
// as buildResultFromEC2 records it.
func TestPrintTable_AcceleratorNominal(t *testing.T) {
	rows := []aws.InstanceTypeResult{
		{
			InstanceType: "trn2.48xlarge", Region: "us-east-1", VCPUs: 192, MemoryMiB: 2048 * 1024, Architecture: "x86_64",
			AccelMemoryMiB: 98304, AccelMemoryGB: 96, AcceleratorCount: 16, AcceleratorModel: "Trainium2",
		},
		{
			InstanceType: "p6-b200.48xlarge", Region: "us-east-1", VCPUs: 192, MemoryMiB: 2048 * 1024, Architecture: "x86_64",
			GPUs: 8, GPUModel: "B200", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: 8 * 183359, GPUMemoryPerMiB: 183359, AccelMemoryMiB: 183359, AccelMemoryGB: 179,
			AcceleratorCount: 8, AcceleratorModel: "B200", AcceleratorManufacturer: "NVIDIA",
		},
		{
			InstanceType: "p6-b300.48xlarge", Region: "us-east-1", VCPUs: 192, MemoryMiB: 4096 * 1024, Architecture: "x86_64",
			GPUs: 8, GPUModel: "B300", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: 8 * 275040, GPUMemoryPerMiB: 275040, AccelMemoryMiB: 275040, AccelMemoryGB: 268,
			AcceleratorCount: 8, AcceleratorModel: "B300", AcceleratorManufacturer: "NVIDIA",
		},
	}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(rows, false, false)
	})
	for _, tt := range []struct{ it, want string }{
		{"trn2.48xlarge", "16|Trainium2|1536 GiB (96/chip)"},
		{"p6-b200.48xlarge", "8|NVIDIA B200|1432 GiB (179/gpu)"},
		{"p6-b300.48xlarge", "8|NVIDIA B300|2144 GiB (268/gpu)"},
	} {
		if row := cells(tableRow(t, out, tt.it)); !strings.Contains(row, "|"+tt.want+"|") {
			t.Errorf("%s row should contain %q, got:\n%s", tt.it, tt.want, row)
		}
	}
}

// TestPrintTable_B200NotLabelledGB pins #244: EC2's 183359 MiB per B200 is
// 179.06 GiB, so the table must never call it "179 GB" or put a GB unit on
// the VRAM header, and the CSV carries the exact GiB beside the nominal size.
func TestPrintTable_B200NotLabelledGB(t *testing.T) {
	rows := []aws.InstanceTypeResult{{
		InstanceType: "p6-b200.48xlarge", Region: "us-east-1", VCPUs: 192, MemoryMiB: 2048 * 1024, Architecture: "x86_64",
		GPUs: 8, GPUModel: "B200", GPUManufacturer: "NVIDIA",
		GPUMemoryMiB: 8 * 183359, GPUMemoryPerMiB: 183359, AccelMemoryMiB: 183359, AccelMemoryGB: 179, AccelMemoryGiB: 179.1,
		AcceleratorCount: 8, AcceleratorModel: "B200", AcceleratorManufacturer: "NVIDIA",
	}}
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(rows, false, false)
	})
	if strings.Contains(out, "VRAM (GB)") || strings.Contains(out, "1432 (") || strings.Contains(out, "179 GB") {
		t.Errorf("B200 VRAM must be labelled GiB, not GB:\n%s", out)
	}
	if row := cells(tableRow(t, out, "p6-b200.48xlarge")); !strings.Contains(row, "|1432 GiB (179/gpu)|") {
		t.Errorf("B200 row should show 1432 GiB (179/gpu), got:\n%s", row)
	}
	csv := captureStdout(t, func() {
		_ = NewPrinter(false).PrintCSV(rows)
	})
	// accelerator_memory_gib is the LAST column, so the columns before it keep
	// the positions they had before #244 for readers that index by position.
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) < 2 {
		t.Fatalf("CSV has no data row:\n%s", csv)
	}
	if !strings.HasSuffix(lines[0], ",accelerator_memory_gb,availability_zones,accelerator_memory_gib") {
		t.Errorf("accelerator_memory_gib must be appended after the existing columns:\n%s", lines[0])
	}
	if !strings.Contains(lines[1], ",NVIDIA B200,179,") || !strings.HasSuffix(lines[1], ",179.1") {
		t.Errorf("B200 CSV should carry nominal 179 and, last, exact 179.1 GiB:\n%s", csv)
	}
}

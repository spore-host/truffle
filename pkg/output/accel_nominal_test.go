package output

import (
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// TestPrintTable_AcceleratorNominal checks the documented per-device sizes
// in the table (#242): trn2.48xlarge's 16 Trainium2 chips are 96 GB each,
// 1536 in all (AWS: 1.5 TB), labelled per chip, not EC2's 512 GiB per
// device; a B200 is 179 GB and a B300 268 GB, AWS's per-instance totals
// (1,432 and 2,144 GB) over 8 GPUs, not 179.1 and 268.6. The AccelMemoryMiB
// is as buildResultFromEC2 records it.
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
		{"trn2.48xlarge", "16|Trainium2|1536 (96/chip)"},
		{"p6-b200.48xlarge", "8|NVIDIA B200|1432 (179/gpu)"},
		{"p6-b300.48xlarge", "8|NVIDIA B300|2144 (268/gpu)"},
	} {
		if row := cells(tableRow(t, out, tt.it)); !strings.Contains(row, "|"+tt.want+"|") {
			t.Errorf("%s row should contain %q, got:\n%s", tt.it, tt.want, row)
		}
	}
}

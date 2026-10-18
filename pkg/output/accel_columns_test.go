package output

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// Device memory as DescribeInstanceTypes reported it on 2026-10-15.
const (
	mibInferentia2 = 32768 // inf2, NeuronInfo
	mibV100p3      = 16384 // p3 (16 GB V100)
	mibV100p3dn    = 32768 // p3dn (32 GB V100)
	mibAI100       = 16000 // dl2q, InferenceAcceleratorInfo
)

// accelRows are an Inferentia2 type with one and with twelve devices, dl2q's
// eight AI100s, and a g5 GPU row, as buildResultFromEC2 fills them (#240).
func accelRows() []aws.InstanceTypeResult {
	return []aws.InstanceTypeResult{
		{
			InstanceType: "inf2.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 16384, Architecture: "x86_64",
			AccelMemoryMiB: mibInferentia2, AccelMemoryGB: 32,
			AcceleratorCount: 1, AcceleratorModel: "Inferentia2",
		},
		{
			InstanceType: "inf2.48xlarge", Region: "us-east-1", VCPUs: 192, MemoryMiB: 786432, Architecture: "x86_64",
			AccelMemoryMiB: mibInferentia2, AccelMemoryGB: 32,
			AcceleratorCount: 12, AcceleratorModel: "Inferentia2",
		},
		{
			InstanceType: "dl2q.24xlarge", Region: "us-east-1", VCPUs: 96, MemoryMiB: 786432, Architecture: "x86_64",
			AccelMemoryMiB: mibAI100, AccelMemoryGB: 16,
			AcceleratorCount: 8, AcceleratorModel: "Qualcomm AI100", AcceleratorManufacturer: "Qualcomm",
		},
		{
			InstanceType: "g5.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 16384, Architecture: "x86_64",
			GPUs: 1, GPUModel: "A10G", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: mibA10G, GPUMemoryPerMiB: mibA10G, AccelMemoryMiB: mibA10G, AccelMemoryGB: 24,
			AcceleratorCount: 1, AcceleratorModel: "A10G", AcceleratorManufacturer: "NVIDIA",
		},
	}
}

// v100Rows are p3 (16 GB V100) and p3dn (32 GB V100) GPU rows.
func v100Rows() []aws.InstanceTypeResult {
	return []aws.InstanceTypeResult{
		{
			InstanceType: "p3.2xlarge", Region: "us-east-1", VCPUs: 8, MemoryMiB: 62464, Architecture: "x86_64",
			GPUs: 1, GPUModel: "V100", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: mibV100p3, GPUMemoryPerMiB: mibV100p3, AccelMemoryMiB: mibV100p3, AccelMemoryGB: 16,
			AcceleratorCount: 1, AcceleratorModel: "V100", AcceleratorManufacturer: "NVIDIA",
		},
		{
			InstanceType: "p3dn.24xlarge", Region: "us-east-1", VCPUs: 96, MemoryMiB: 786432, Architecture: "x86_64",
			GPUs: 8, GPUModel: "V100", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: 8 * mibV100p3dn, GPUMemoryPerMiB: mibV100p3dn, AccelMemoryMiB: mibV100p3dn, AccelMemoryGB: 32,
			AcceleratorCount: 8, AcceleratorModel: "V100", AcceleratorManufacturer: "NVIDIA",
		},
	}
}

// TestPrintTable_NeuronAccelerators checks Inferentia2 and AI100 rows show
// their count, model and per-device memory in the accelerator columns rather
// than "-", under "Accel" headers since they are not GPUs (#240).
func TestPrintTable_NeuronAccelerators(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(accelRows(), TableOptions{ShowGPURatios: true})
	})
	for _, h := range []string{"Accel ", "Accel Model", "Accel mem", "vCPU/Accel"} {
		if !strings.Contains(out, h) {
			t.Errorf("header %q missing:\n%s", h, out)
		}
	}
	if strings.Contains(out, "VRAM") || strings.Contains(out, "GPU Model") {
		t.Errorf("a table with Inferentia rows should not head the columns GPU/VRAM:\n%s", out)
	}
	for _, tt := range []struct{ it, want string }{
		{"inf2.xlarge", "1|Inferentia2|32 GiB (32/chip)"},
		{"inf2.48xlarge", "12|Inferentia2|384 GiB (32/chip)"},
		{"dl2q.24xlarge", "8|Qualcomm AI100|128 GB (16/dev)"},
		{"g5.xlarge", "1|NVIDIA A10G|24 GB (24/gpu)"},
	} {
		if row := cells(tableRow(t, out, tt.it)); !strings.Contains(row, "|"+tt.want+"|") {
			t.Errorf("%s row should contain %q, got:\n%s", tt.it, tt.want, row)
		}
	}
	if row := tableRow(t, out, "inf2.48xlarge"); !strings.Contains(row, " 16.0 ") {
		t.Errorf("inf2.48xlarge vCPU per accelerator should be 16.0:\n%s", row)
	}
}

// TestPrintTable_V100PerFamily checks the V100's memory per family: 16 GB on
// p3, 32 GB on p3dn (#240).
func TestPrintTable_V100PerFamily(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(v100Rows(), false, false)
	})
	if !strings.Contains(out, "VRAM") || strings.Contains(out, "Accel") {
		t.Errorf("a GPU-only table keeps the GPU headers:\n%s", out)
	}
	if row := tableRow(t, out, "p3.2xlarge"); !strings.Contains(row, " 16 GiB (16/gpu) ") {
		t.Errorf("p3 V100 should be 16 GB, got:\n%s", row)
	}
	if row := tableRow(t, out, "p3dn.24xlarge"); !strings.Contains(row, " 256 GiB (32/gpu) ") {
		t.Errorf("p3dn V100 should be 32 GB each, got:\n%s", row)
	}
}

// TestPrintCSV_NeuronAccelerators checks the CSV carries the same count,
// model and per-device GB, in columns named for the JSON fields (#240).
func TestPrintCSV_NeuronAccelerators(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintCSV(accelRows())
	})
	want := "instance_type,region,vcpus,memory_gib,architecture,accelerator_count,accelerator_model,accelerator_memory_gb,availability_zones,accelerator_memory_gib\n" +
		"inf2.xlarge,us-east-1,4,16.0,x86_64,1,Inferentia2,32,,32\n" +
		"inf2.48xlarge,us-east-1,192,768.0,x86_64,12,Inferentia2,32,,32\n" +
		"dl2q.24xlarge,us-east-1,96,768.0,x86_64,8,Qualcomm AI100,16,,15.6\n" +
		"g5.xlarge,us-east-1,4,16.0,x86_64,1,NVIDIA A10G,24,,22.4\n"
	if out != want {
		t.Errorf("CSV mismatch\n got: %q\nwant: %q", out, want)
	}
}

// TestPrintJSON_NeuronAccelerators checks JSON names the device beside its
// memory, and leaves gpus unset for an Inferentia (#240).
func TestPrintJSON_NeuronAccelerators(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintJSON(accelRows())
	})
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	inf := got[1]
	if inf["accelerator_count"] != float64(12) || inf["accelerator_model"] != "Inferentia2" ||
		inf["accelerator_memory_gb"] != float64(32) {
		t.Errorf("inf2 JSON = %v", inf)
	}
	if _, ok := inf["gpus"]; ok {
		t.Errorf("inf2 JSON should have no gpus field: %v", inf)
	}
}

// cells joins a table row's trimmed cells with "|".
func cells(row string) string {
	parts := strings.Split(row, "│")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, "|")
}

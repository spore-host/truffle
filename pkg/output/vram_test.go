package output

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// MiB per GPU as DescribeInstanceTypes reported them on 2026-10-15
// (GpuInfo.Gpus[].MemoryInfo.SizeInMiB).
const (
	mibA10G = 22888 // g5: 24 GB as sold, 22.35 GiB
	mibH100 = 81920 // p5: 80 GiB
)

// vramRows are real-shaped GPU rows: one- and four-GPU A10Gs and an
// eight-GPU H100.
func vramRows() []aws.InstanceTypeResult {
	return []aws.InstanceTypeResult{
		{
			InstanceType: "g5.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 16384, Architecture: "x86_64",
			GPUs: 1, GPUModel: "A10G", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: mibA10G, GPUMemoryPerMiB: mibA10G, AccelMemoryMiB: mibA10G, AccelMemoryGB: 24,
		},
		{
			InstanceType: "g5.12xlarge", Region: "us-east-1", VCPUs: 48, MemoryMiB: 196608, Architecture: "x86_64",
			GPUs: 4, GPUModel: "A10G", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: 4 * mibA10G, GPUMemoryPerMiB: mibA10G, AccelMemoryMiB: mibA10G, AccelMemoryGB: 24,
		},
		{
			InstanceType: "p5.48xlarge", Region: "us-east-1", VCPUs: 192, MemoryMiB: 2097152, Architecture: "x86_64",
			GPUs: 8, GPUModel: "H100", GPUManufacturer: "NVIDIA",
			GPUMemoryMiB: 8 * mibH100, GPUMemoryPerMiB: mibH100, AccelMemoryMiB: mibH100, AccelMemoryGB: 80,
		},
	}
}

// tableRow returns the table line for instance type it.
func tableRow(t *testing.T, out, it string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, " "+it+" ") {
			return l
		}
	}
	t.Fatalf("no row for %s:\n%s", it, out)
	return ""
}

// TestPrintTable_VRAMNominalGB checks the VRAM column shows the nominal GB
// figure the per-GPU memory filter compares (#238): an A10G is 24 (as sold),
// not 22 (GiB), so a "gpu 24gb" query doesn't list it as 22.
func TestPrintTable_VRAMNominalGB(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTable(vramRows(), false, false)
	})
	// The header carries no unit; each cell carries its own (#244).
	if !strings.Contains(out, "VRAM") || strings.Contains(out, "VRAM (") {
		t.Errorf("VRAM header should carry no unit:\n%s", out)
	}
	if row := tableRow(t, out, "g5.xlarge"); !strings.Contains(row, " 24 GB (24/gpu) ") {
		t.Errorf("A10G row should show 24 GB VRAM, got:\n%s", row)
	}
	// 4 x 22888 MiB is 89.4 GiB, close enough to a whole GiB to fool a
	// nominal reading of the total; the total is 4 x 24 GB.
	if row := tableRow(t, out, "g5.12xlarge"); !strings.Contains(row, " 96 GB (24/gpu) ") {
		t.Errorf("4 x A10G row should show 96 GB total, 24 per GPU, got:\n%s", row)
	}
	if row := tableRow(t, out, "p5.48xlarge"); !strings.Contains(row, " 640 GiB (80/gpu) ") {
		t.Errorf("H100 row should show 640 GiB total, 80 per GPU, got:\n%s", row)
	}
}

// TestPrintCSV_VRAMNominalGB checks the CSV carries the same per-GPU figure.
func TestPrintCSV_VRAMNominalGB(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintCSV(vramRows())
	})
	want := "instance_type,region,vcpus,memory_gib,architecture,accelerator_count,accelerator_model,accelerator_memory_gb,accelerator_memory_gib,availability_zones\n" +
		"g5.xlarge,us-east-1,4,16.0,x86_64,1,NVIDIA A10G,24,22.4,\n" +
		"g5.12xlarge,us-east-1,48,192.0,x86_64,4,NVIDIA A10G,24,22.4,\n" +
		"p5.48xlarge,us-east-1,192,2048.0,x86_64,8,NVIDIA H100,80,80,\n"
	if out != want {
		t.Errorf("CSV mismatch\n got: %q\nwant: %q", out, want)
	}
}

// TestPrintJSON_VRAMKeepsMiB checks JSON keeps the raw MiB for machine use
// alongside the nominal GB.
func TestPrintJSON_VRAMKeepsMiB(t *testing.T) {
	out := captureStdout(t, func() {
		_ = NewPrinter(false).PrintJSON(vramRows())
	})
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got[0]["accelerator_memory_mib"] != float64(mibA10G) || got[0]["accelerator_memory_gb"] != float64(24) {
		t.Errorf("A10G JSON = %v", got[0])
	}
	if got[2]["accelerator_memory_mib"] != float64(mibH100) || got[2]["accelerator_memory_gb"] != float64(80) {
		t.Errorf("H100 JSON = %v", got[2])
	}
}

// TestPrintCPUOnly_DemoUnchanged pins the output of the demo query
// `truffle find "amd turin 16 cores 64gb"` (no GPU rows) as it was before
// #238: no VRAM or GPU columns appear in any format.
func TestPrintCPUOnly_DemoUnchanged(t *testing.T) {
	rs := []aws.InstanceTypeResult{
		{InstanceType: "m8a.4xlarge", Region: "us-west-2", VCPUs: 16, PhysicalCores: 16, MemoryMiB: 65536, Architecture: "x86_64", OnDemandPrice: 0.9738},
		{InstanceType: "c8a.8xlarge", Region: "us-west-2", VCPUs: 32, PhysicalCores: 32, MemoryMiB: 65536, Architecture: "x86_64", OnDemandPrice: 1.7243},
	}
	table := captureStdout(t, func() {
		_ = NewPrinter(false).PrintTableWithOptions(rs, TableOptions{ShowPrice: true})
	})
	if strings.Contains(table, "VRAM") || strings.Contains(table, "GPU") {
		t.Errorf("CPU-only table grew GPU columns:\n%s", table)
	}
	for _, it := range []string{"m8a.4xlarge", "c8a.8xlarge"} {
		if row := tableRow(t, table, it); strings.Count(row, "│") != 7 {
			t.Errorf("%s row should have 6 columns:\n%s", it, row)
		}
	}

	csvOut := captureStdout(t, func() { _ = NewPrinter(false).PrintCSV(rs) })
	wantCSV := "instance_type,region,vcpus,memory_gib,architecture,on_demand_price,availability_zones\n" +
		"m8a.4xlarge,us-west-2,16,64.0,x86_64,0.9738,\n" +
		"c8a.8xlarge,us-west-2,32,64.0,x86_64,1.7243,\n"
	if csvOut != wantCSV {
		t.Errorf("CSV changed\n got: %q\nwant: %q", csvOut, wantCSV)
	}

	jsonOut := captureStdout(t, func() { _ = NewPrinter(false).PrintJSON(rs) })
	wantJSON := `[
  {
    "instance_type": "m8a.4xlarge",
    "region": "us-west-2",
    "vcpus": 16,
    "physical_cores": 16,
    "memory_mib": 65536,
    "architecture": "x86_64",
    "on_demand_price": 0.9738
  },
  {
    "instance_type": "c8a.8xlarge",
    "region": "us-west-2",
    "vcpus": 32,
    "physical_cores": 32,
    "memory_mib": 65536,
    "architecture": "x86_64",
    "on_demand_price": 1.7243
  }
]
`
	if jsonOut != wantJSON {
		t.Errorf("JSON changed\n got: %s\nwant: %s", jsonOut, wantJSON)
	}
}

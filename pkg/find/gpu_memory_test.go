package find

import (
	"context"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/aws/awsmock"
)

// TestMemoryReading checks which memory a size means (#236): a bare size is
// per-GPU memory in a GPU or accelerator query and host memory otherwise;
// "ram"/"memory" always mean host memory and "vram"/"gpu memory" per-GPU
// memory.
func TestMemoryReading(t *testing.T) {
	tests := []struct {
		query    string
		wantHost float64
		wantGPU  float64
		wantNote bool
	}{
		{"gpu 24gb", 0, 24, true},
		{"gpu 24 gb", 0, 24, true},
		{"gpus 24gb", 0, 24, true},
		{"h100 80gb", 0, 80, true},
		{"a100-80gb", 0, 80, true},
		{"g5 24gb", 0, 24, true},
		{"inference 24gb", 0, 24, true},
		{"2 gpus 24gb", 0, 24, true},
		{"a10g 64gb ram", 64, 0, false},
		{"a10g 64gb memory", 64, 0, false},
		{"a10g 64 gb ram", 64, 0, false},
		{"a10g 64gb host memory", 64, 0, false},
		{"24gb vram", 0, 24, false},
		{"24gb gpu memory", 0, 24, false},
		{"gpu 24gb per gpu", 0, 24, false},
		{"a10g 24gb 64gb ram", 64, 24, true},
		{"a10g 24gb vram 64gb", 64, 24, false},
		{"amd turin 16 cores 64gb", 64, 0, false},
		{"amd 16 cores 64gb memory", 64, 0, false},
		{"graviton 32gb", 32, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			pq := mustParse(t, tt.query)
			if pq.MinMemory != tt.wantHost {
				t.Errorf("MinMemory = %v, want %v", pq.MinMemory, tt.wantHost)
			}
			if pq.MinGPUMemory != tt.wantGPU {
				t.Errorf("MinGPUMemory = %v, want %v", pq.MinGPUMemory, tt.wantGPU)
			}
			if unknown := pq.UnknownTerms(); len(unknown) > 0 {
				t.Errorf("unknown terms %v", unknown)
			}
			note := pq.MemoryReadingNote()
			if (note != "") != tt.wantNote {
				t.Errorf("note = %q, want note: %v", note, tt.wantNote)
			}
			sc, err := pq.BuildCriteria(false)
			if err != nil {
				t.Fatal(err)
			}
			if sc.FilterOptions.MinGPUMemory != tt.wantGPU || sc.FilterOptions.MinMemory != tt.wantHost {
				t.Errorf("FilterOptions MinGPUMemory = %v, MinMemory = %v; want %v, %v",
					sc.FilterOptions.MinGPUMemory, sc.FilterOptions.MinMemory, tt.wantGPU, tt.wantHost)
			}
		})
	}
}

func TestMemoryReadingNote(t *testing.T) {
	got := mustParse(t, "gpu 24gb").MemoryReadingNote()
	if !strings.Contains(got, `"24gb"`) || !strings.Contains(got, "per GPU") || !strings.Contains(got, `"24gb ram"`) {
		t.Errorf("note = %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("note is more than one line: %q", got)
	}
}

// gpuCatalog is one size of each accelerator family, with the per-device
// memory DescribeInstanceTypes reported on 2026-10-15
// (GpuInfo.Gpus[].MemoryInfo.SizeInMiB, or NeuronInfo for inf2/trn1).
func gpuCatalog() []aws.InstanceTypeResult {
	mk := func(it string, hostGiB, accelMiB int64) aws.InstanceTypeResult {
		return aws.InstanceTypeResult{
			InstanceType: it, Region: "us-east-1", VCPUs: 8, MemoryMiB: hostGiB * 1024,
			Architecture: "x86_64", AccelMemoryMiB: accelMiB, GPUMemoryPerMiB: accelMiB,
		}
	}
	return []aws.InstanceTypeResult{
		mk("g4dn.xlarge", 16, 16384),    // T4
		mk("g5g.xlarge", 8, 16384),      // T4g
		mk("g6f.4xlarge", 64, 11444),    // half an L4
		mk("g5.xlarge", 16, 22888),      // A10G
		mk("g5.4xlarge", 64, 22888),     // A10G
		mk("g6.xlarge", 16, 22888),      // L4
		mk("g6e.xlarge", 32, 45776),     // L40S
		mk("p4d.24xlarge", 1152, 40960), // A100 40 GB
		mk("p4de.24xlarge", 1152, 81920),
		mk("p5.48xlarge", 2048, 81920), // H100
		mk("inf2.xlarge", 16, 32768),   // Inferentia2
		mk("m7i.2xlarge", 32, 0),
		mk("m8a.4xlarge", 64, 0),
	}
}

// search runs a query against gpuCatalog through BuildCriteria and the mock
// search, which applies the same filters as the live one.
func search(t *testing.T, query string) []string {
	t.Helper()
	sc, err := mustParse(t, query).BuildCriteria(false)
	if err != nil {
		t.Fatal(err)
	}
	f := &awsmock.Finder{Instances: gpuCatalog()}
	rs, err := f.SearchInstanceTypes(context.Background(), []string{"us-east-1"}, sc.InstanceTypePattern, sc.FilterOptions)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rs {
		out = append(out, r.InstanceType)
	}
	sort.Strings(out)
	return out
}

// TestGPUMemorySearch checks the results of GPU-memory queries (#236).
func TestGPUMemorySearch(t *testing.T) {
	tests := []struct {
		query   string
		want    []string // must be present
		notWant []string // must be absent
	}{
		{"gpu 24gb",
			[]string{"g5.xlarge", "g6.xlarge", "g6e.xlarge", "p4d.24xlarge", "p5.48xlarge", "inf2.xlarge"},
			[]string{"g4dn.xlarge", "g5g.xlarge", "g6f.4xlarge", "m7i.2xlarge"}},
		{"h100 80gb", []string{"p5.48xlarge"}, nil},
		{"a100 80gb", []string{"p4de.24xlarge"}, []string{"p4d.24xlarge"}},
		{"a10g 64gb ram", []string{"g5.4xlarge"}, []string{"g5.xlarge"}},
		{"24gb vram", []string{"g5.xlarge", "p5.48xlarge"}, []string{"g4dn.xlarge", "m7i.2xlarge", "m8a.4xlarge"}},
		{"gpu 16gb", []string{"g4dn.xlarge", "g5g.xlarge", "g5.xlarge"}, []string{"g6f.4xlarge"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := search(t, tt.query)
			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("results %v lack %s", got, w)
				}
			}
			for _, n := range tt.notWant {
				if slices.Contains(got, n) {
					t.Errorf("results %v include %s", got, n)
				}
			}
		})
	}

	// --exact: only GPUs sold with exactly 24 GB.
	pq := mustParse(t, "gpu 24gb")
	pq.ExactMatch = true
	sc, err := pq.BuildCriteria(false)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.FilterOptions.ExactGPUMemory {
		t.Error("--exact does not make the GPU memory exact")
	}
}

func TestExplainGPUMemory(t *testing.T) {
	r := aws.InstanceTypeResult{InstanceType: "g5.xlarge", AccelMemoryMiB: 22888}
	reasons := ExplainMatch(r, mustParse(t, "gpu 24gb"))
	if !slices.Contains(reasons, "GPU memory: 24 GB >= 24 GB") {
		t.Errorf("reasons = %v, want GPU memory: 24 GB >= 24 GB", reasons)
	}
}

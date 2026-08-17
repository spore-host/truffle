package metadata

import (
	"testing"
)

func TestGPUDatabase(t *testing.T) {
	tests := []struct {
		name             string
		gpuName          string
		wantVendor       string
		wantUseCase      string
		wantMinMemory    int
		wantMinInstances int
	}{
		{
			name:             "a100",
			gpuName:          "a100",
			wantVendor:       "nvidia",
			wantUseCase:      "training",
			wantMinMemory:    40,
			wantMinInstances: 1,
		},
		{
			name:             "h100",
			gpuName:          "h100",
			wantVendor:       "nvidia",
			wantUseCase:      "training",
			wantMinMemory:    80,
			wantMinInstances: 1,
		},
		{
			name:             "v100",
			gpuName:          "v100",
			wantVendor:       "nvidia",
			wantUseCase:      "training",
			wantMinMemory:    16,
			wantMinInstances: 2,
		},
		{
			name:             "t4",
			gpuName:          "t4",
			wantVendor:       "nvidia",
			wantUseCase:      "inference",
			wantMinMemory:    16,
			wantMinInstances: 5,
		},
		{
			name:             "inferentia",
			gpuName:          "inferentia",
			wantVendor:       "aws",
			wantUseCase:      "inference",
			wantMinMemory:    8,
			wantMinInstances: 2,
		},
		{
			name:             "trainium",
			gpuName:          "trainium",
			wantVendor:       "aws",
			wantUseCase:      "training",
			wantMinMemory:    32,
			wantMinInstances: 2,
		},
		{
			// g7 = NVIDIA RTX PRO 4500 (spawn#384 follow-up: g7 was unknown).
			name:             "rtx pro 4500",
			gpuName:          "rtx pro 4500",
			wantVendor:       "nvidia",
			wantUseCase:      "graphics",
			wantMinMemory:    32,
			wantMinInstances: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, ok := GPUDatabase[tt.gpuName]
			if !ok {
				t.Fatalf("GPU %q not found in database", tt.gpuName)
			}

			if info.Vendor != tt.wantVendor {
				t.Errorf("vendor = %v, want %v", info.Vendor, tt.wantVendor)
			}

			if info.UseCase != tt.wantUseCase {
				t.Errorf("use case = %v, want %v", info.UseCase, tt.wantUseCase)
			}

			if info.MemoryGB < tt.wantMinMemory {
				t.Errorf("memory = %v, want >= %v", info.MemoryGB, tt.wantMinMemory)
			}

			if len(info.InstanceTypes) < tt.wantMinInstances {
				t.Errorf("instance types count = %v, want >= %v",
					len(info.InstanceTypes), tt.wantMinInstances)
			}
		})
	}
}

func TestGPUAliases(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"inf", "inferentia"},
		{"inf1", "inferentia"},
		{"inf2", "inferentia2"},
		{"trn", "trainium"},
		{"trn1", "trainium"},
		{"a10", "a10g"},
	}

	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, ok := GPUAliases[tt.alias]
			if !ok {
				t.Fatalf("alias %q not found", tt.alias)
			}
			if got != tt.want {
				t.Errorf("GPUAliases[%q] = %v, want %v", tt.alias, got, tt.want)
			}
		})
	}
}

func TestGetGPUsByVendor(t *testing.T) {
	tests := []struct {
		vendor  string
		wantMin int
	}{
		{"nvidia", 5},
		{"aws", 3},
		{"amd", 1},
	}

	for _, tt := range tests {
		t.Run(tt.vendor, func(t *testing.T) {
			gpus := GetGPUsByVendor(tt.vendor)
			if len(gpus) < tt.wantMin {
				t.Errorf("GetGPUsByVendor(%q) returned %d GPUs, want >= %d",
					tt.vendor, len(gpus), tt.wantMin)
			}
		})
	}
}

func TestGetGPUsByUseCase(t *testing.T) {
	tests := []struct {
		useCase string
		wantMin int
	}{
		{"training", 3},
		{"inference", 4},
	}

	for _, tt := range tests {
		t.Run(tt.useCase, func(t *testing.T) {
			gpus := GetGPUsByUseCase(tt.useCase)
			if len(gpus) < tt.wantMin {
				t.Errorf("GetGPUsByUseCase(%q) returned %d GPUs, want >= %d",
					tt.useCase, len(gpus), tt.wantMin)
			}
		})
	}
}

// TestMIGCapable pins the GPU chips NVIDIA's own MIG User Guide "Supported
// GPUs" table (checked 2026-08) lists as MIG-capable, and the ones it does
// NOT list (#143). L4/L40S/T4/A10G are Ada-generation inference/graphics
// chips absent from that table entirely; V100 predates MIG (Ampere+ only);
// B300 shares the p6 family prefix with B200 but is not listed.
func TestMIGCapable(t *testing.T) {
	migCapable := []string{"a100", "h100", "h200", "b200", "rtx pro server 6000", "rtx pro 4500"}
	for _, key := range migCapable {
		t.Run(key, func(t *testing.T) {
			info, ok := GPUDatabase[key]
			if !ok {
				t.Fatalf("GPU %q not found in database", key)
			}
			if !info.MIGCapable {
				t.Errorf("GPUDatabase[%q].MIGCapable = false, want true", key)
			}
			if info.MaxMIGInstances <= 0 {
				t.Errorf("GPUDatabase[%q].MaxMIGInstances = %d, want > 0", key, info.MaxMIGInstances)
			}
		})
	}

	notMIGCapable := []string{"l4", "l40s", "t4", "a10g", "v100", "b300"}
	for _, key := range notMIGCapable {
		t.Run(key, func(t *testing.T) {
			info, ok := GPUDatabase[key]
			if !ok {
				t.Fatalf("GPU %q not found in database", key)
			}
			if info.MIGCapable {
				t.Errorf("GPUDatabase[%q].MIGCapable = true, want false", key)
			}
		})
	}
}

func TestGetMIGCapableFamilies(t *testing.T) {
	families := GetMIGCapableFamilies()
	mustInclude := []string{"p4d", "p4de", "p5", "p5e", "p5en", "p6-b200", "g7e", "g7"}
	familySet := make(map[string]bool, len(families))
	for _, f := range families {
		familySet[f] = true
	}
	for _, f := range mustInclude {
		if !familySet[f] {
			t.Errorf("GetMIGCapableFamilies() missing %q, got %v", f, families)
		}
	}
	mustExclude := []string{"g6", "g6e", "g5", "g4dn", "p3", "p6-b300"}
	for _, f := range mustExclude {
		if familySet[f] {
			t.Errorf("GetMIGCapableFamilies() should not include %q (not MIG-capable), got %v", f, families)
		}
	}
}

func TestIsMIGSupported(t *testing.T) {
	if !IsMIGSupported("p5") {
		t.Error("IsMIGSupported(\"p5\") = false, want true (H100)")
	}
	if IsMIGSupported("g6") {
		t.Error("IsMIGSupported(\"g6\") = true, want false (L4 is not MIG-capable)")
	}
}

// TestMPSAlias confirms "mps" resolves to the same family list as the
// "nvidia" vendor entry — MPS is a CUDA runtime feature available on any
// NVIDIA GPU, not a hardware capability tied to a specific chip (#143).
func TestMPSAlias(t *testing.T) {
	canonical, ok := GPUAliases["mps"]
	if !ok {
		t.Fatal("GPUAliases[\"mps\"] not found")
	}
	if canonical != "nvidia" {
		t.Errorf("GPUAliases[\"mps\"] = %q, want \"nvidia\"", canonical)
	}
}

// TestBareP6FamilyFixed regression-guards #143's b200/b300 family-prefix fix:
// the real AWS instance-type family is "p6-b200"/"p6-b300", not bare "p6",
// which never matched any real instance type via the fuzzy-family path.
func TestBareP6FamilyFixed(t *testing.T) {
	for _, key := range []string{"b200", "b300"} {
		info := GPUDatabase[key]
		for _, f := range info.Families {
			if f == "p6" {
				t.Errorf("GPUDatabase[%q].Families still contains bare \"p6\", which matches no real instance type", key)
			}
		}
	}
	nvidia := GPUDatabase["nvidia"]
	for _, f := range nvidia.Families {
		if f == "p6" {
			t.Error("GPUDatabase[\"nvidia\"].Families still contains bare \"p6\"")
		}
	}
}

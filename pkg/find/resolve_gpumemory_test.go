package find

import (
	"errors"
	"sort"
	"testing"

	"github.com/spore-host/truffle/pkg/metadata"
)

// A memory size in a card name is per-GPU memory and is applied (#245):
// "A100-80GB" must not include p4d.24xlarge, whose A100s have 40 GB, or an
// 80 GB job is placed on GPUs too small for it. The size is a minimum
// (MinGPUMemory, #236), so the smaller variant's name keeps both.
func TestResolveCard_AppliesGPUMemory(t *testing.T) {
	for _, c := range []struct {
		card string
		want []string
	}{
		{"A100-80GB", []string{"p4de.24xlarge"}},
		{"a100-80gb", []string{"p4de.24xlarge"}},
		{"a100 80gb", []string{"p4de.24xlarge"}},
		{"A100-40GB", []string{"p4d.24xlarge", "p4de.24xlarge"}},
		{"A100", []string{"p4d.24xlarge", "p4de.24xlarge"}},
	} {
		got, err := ResolveCard(c.card)
		if err != nil {
			t.Fatalf("ResolveCard(%q): %v", c.card, err)
		}
		if !stringSlicesEqual(got, c.want) {
			t.Errorf("ResolveCard(%q) = %v, want %v", c.card, got, c.want)
		}
	}
}

// V100 is the other model whose memory differs by family: 16 GB on p3, 32 GB
// on p3dn.
func TestResolveCard_GPUMemoryTellsV100VariantsApart(t *testing.T) {
	got, err := ResolveCard("V100-32GB")
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range got {
		if extractFamily(inst) != "p3dn" {
			t.Errorf("ResolveCard(V100-32GB) includes %s; only p3dn has 32 GB V100s (got %v)", inst, got)
		}
	}
	if len(got) == 0 {
		t.Error("ResolveCard(V100-32GB) returned nothing; p3dn has 32 GB V100s")
	}
}

// A size no variant has is no match, not the card with the size ignored.
func TestResolveCard_GPUMemoryNoVariantIsNoMatch(t *testing.T) {
	got, err := ResolveCard("A100-160GB")
	if !errors.Is(err, ErrNoMatch) {
		t.Errorf("ResolveCard(A100-160GB) = %v, %v; want ErrNoMatch (no A100 has 160 GB)", got, err)
	}
}

// Every instance type the GPU catalog names must have a known per-GPU memory,
// except a fractional-GPU family's, whose partition is smaller than the model's
// memory. One that didn't would be dropped from every memory-suffixed card
// query, a silent narrowing. Discovered from the catalog, not listed by hand.
func TestEveryCatalogGPUInstanceHasAKnownGPUMemory(t *testing.T) {
	var missing []string
	n := 0
	for name, info := range metadata.GPUDatabase {
		for _, inst := range info.InstanceTypes {
			n++
			if metadata.IsFractionalGPUFamily(extractFamily(inst)) {
				continue
			}
			if _, ok := instanceGPUMemoryMiB(inst, []string{name}); !ok {
				missing = append(missing, name+": "+inst)
			}
		}
	}
	if n == 0 {
		t.Fatal("GPUDatabase names no instance types; the discovery is broken")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d of %d catalog GPU instance types have no known per-GPU memory, so a memory-suffixed card would drop them:\n%v", len(missing), n, missing)
	}
}

// A fractional L4 gets a slice of the GPU (2861 MiB on g6f.large), so a request
// for a 24 GB L4 must not be offered one. Without a suffix it still is.
func TestResolveCard_GPUMemoryDropsFractionalGPUs(t *testing.T) {
	whole, err := ResolveCard("L4-24GB")
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range whole {
		if metadata.IsFractionalGPUFamily(extractFamily(inst)) {
			t.Errorf("ResolveCard(L4-24GB) offers fractional %s", inst)
		}
	}
	all, err := ResolveCard("L4")
	if err != nil {
		t.Fatal(err)
	}
	frac := 0
	for _, inst := range all {
		if metadata.IsFractionalGPUFamily(extractFamily(inst)) {
			frac++
		}
	}
	if frac == 0 || len(whole) == 0 || len(whole) >= len(all) {
		t.Errorf("L4: %d with the suffix, %d without (%d fractional); want whole L4s kept and only the fractional ones dropped", len(whole), len(all), frac)
	}
}

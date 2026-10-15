package metadata

import "testing"

func TestVendorOfFamily(t *testing.T) {
	for family, want := range map[string]string{
		"x8aedz": "amd", "g5": "amd", "t3": "intel", "p5en": "intel",
		"r9g": "aws", "mac2": "", "nosuch": "",
	} {
		if got := VendorOfFamily(family); got != want {
			t.Errorf("VendorOfFamily(%q) = %q, want %q", family, got, want)
		}
	}

	// A family only a codename entry lists still gets that entry's vendor.
	saved := ProcessorDatabase["turin"]
	defer func() { ProcessorDatabase["turin"] = saved }()
	entry := saved
	entry.Families = append(append([]string(nil), saved.Families...), "z9a")
	ProcessorDatabase["turin"] = entry
	if got := VendorOfFamily("z9a"); got != "amd" {
		t.Errorf("VendorOfFamily(z9a) = %q, want amd from the turin entry", got)
	}
}

// TestFamilyVendorsCoverCodenameFamilies: every family a codename entry
// lists is in familyVendors, so vendor membership has one source.
func TestFamilyVendorsCoverCodenameFamilies(t *testing.T) {
	for key, info := range ProcessorDatabase {
		for _, f := range info.Families {
			if familyVendors[f] != info.Vendor {
				t.Errorf("%q lists %s, but familyVendors has %q for it", key, f, familyVendors[f])
			}
		}
	}
}

func TestIsAcceleratorFamily(t *testing.T) {
	for f, want := range map[string]bool{
		"g5": true, "p5en": true, "trn1": true, "inf2": true, "f2": true, "vt1": true, "g5g": true,
		"m8a": false, "x8aedz": false, "t3": false, "nosuch": false,
	} {
		if got := IsAcceleratorFamily(f); got != want {
			t.Errorf("IsAcceleratorFamily(%q) = %v, want %v", f, got, want)
		}
	}
	// Every accelerator family has a host-CPU vendor, so "amd a10g" style
	// queries can reach it.
	for f := range acceleratorFamilies {
		if familyVendors[f] == "" {
			t.Errorf("accelerator family %s has no vendor", f)
		}
	}
}

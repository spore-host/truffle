package cmd

import (
	"testing"

	"github.com/spore-host/truffle/pkg/quotas"
)

// TestIncreaseRequestFor_ZeroQuotaIsRequested is the truffle#171 regression guard.
//
// A zero quota is the single most common reason to run `truffle quotas --request`
// — new accounts default the GPU families to 0 — and it used to produce NO command
// at all, because `quota == 0` sat in the skip condition despite the comment above
// it saying "zero or nearly full". That also made the per-family starting-value
// block below it dead code, so this test asserts those values are now reachable.
func TestIncreaseRequestFor_ZeroQuotaIsRequested(t *testing.T) {
	cases := []struct {
		family      quotas.QuotaFamily
		wantDesired int32
	}{
		{quotas.FamilyP, 192}, // one p5.48xlarge is 192 vCPUs, so 32 would be useless
		{quotas.FamilyG, 128},
		{quotas.FamilyStandard, 32},
		{quotas.FamilyTrn, 32},
		{quotas.FamilyDL, 32},
		{quotas.FamilyX, 32},
		{quotas.FamilyF, 32},
		{quotas.FamilyInf, 32},
	}
	for _, c := range cases {
		t.Run(string(c.family), func(t *testing.T) {
			desired, needed := increaseRequestFor(c.family, 0, 0)
			if !needed {
				t.Fatalf("a zero %s quota must produce a request — that's the most common reason to ask (#171)", c.family)
			}
			if desired != c.wantDesired {
				t.Errorf("desired = %d, want %d", desired, c.wantDesired)
			}
		})
	}
}

// TestIncreaseRequestFor_HeadroomCases covers the non-zero rungs: plenty of
// headroom is skipped, a nearly-full quota is requested, and the doubling floor
// applies.
func TestIncreaseRequestFor_HeadroomCases(t *testing.T) {
	cases := []struct {
		name        string
		quota       int32
		usage       int32
		wantNeeded  bool
		wantDesired int32
	}{
		// 64 total, 8 used → 87% free, plenty of room.
		{"plenty of headroom", 64, 8, false, 0},
		// 64 total, 56 used → 12.5% free, under the 25% threshold.
		{"nearly full", 64, 56, true, 128},
		// Exactly at the threshold is not ">25%", so it requests.
		{"exactly 25% free", 64, 48, true, 128},
		// Fully consumed.
		{"exhausted", 64, 64, true, 128},
		// Over-consumed (quota reduced under a running fleet): available is
		// negative, which must not read as "plenty".
		{"over-consumed", 64, 80, true, 128},
		// Doubling floor: 8*2=16 is below the 32 floor.
		{"tiny quota hits the floor", 8, 8, true, 32},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			desired, needed := increaseRequestFor(quotas.FamilyStandard, c.quota, c.usage)
			if needed != c.wantNeeded {
				t.Fatalf("needed = %v, want %v (quota=%d usage=%d)", needed, c.wantNeeded, c.quota, c.usage)
			}
			if needed && desired != c.wantDesired {
				t.Errorf("desired = %d, want %d", desired, c.wantDesired)
			}
		})
	}
}

// TestIncreaseRequestFor_ZeroQuotaNeverDividesByZero guards the ordering: the
// headroom ratio divides by quota, so the zero case must be handled before it.
// A panic here would mean someone reordered the checks.
func TestIncreaseRequestFor_ZeroQuotaNeverDividesByZero(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero quota must not reach the headroom ratio: %v", r)
		}
	}()
	for _, usage := range []int32{0, 5} {
		if _, needed := increaseRequestFor(quotas.FamilyG, 0, usage); !needed {
			t.Errorf("zero quota with usage=%d must still be requested", usage)
		}
	}
}

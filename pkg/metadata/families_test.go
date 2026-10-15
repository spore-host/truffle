package metadata

import "testing"

func TestIsInstanceFamily(t *testing.T) {
	for _, f := range []string{"g5", "x8aedz", "m7i-flex", "p6-b200", "u7in-16tb", "mac2-m2pro", "h1", "x1e"} {
		if !IsInstanceFamily(f) {
			t.Errorf("IsInstanceFamily(%q) = false", f)
		}
	}
	for _, f := range []string{"", "g5.xlarge", "zzqx", "graviton"} {
		if IsInstanceFamily(f) {
			t.Errorf("IsInstanceFamily(%q) = true", f)
		}
	}
}

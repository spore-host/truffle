package cmd

import "testing"

func TestDescribeUnknownTerms(t *testing.T) {
	got := describeUnknownTerms([]string{"gravitn", "zzqx"})
	want := `"gravitn" (did you mean "graviton"?), "zzqx"`
	if got != want {
		t.Errorf("describeUnknownTerms = %s, want %s", got, want)
	}
}

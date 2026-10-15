package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestListRegion pins #202: list queries the first --region / --regions given,
// else the configured region, and us-east-1 only as a last resort (saying so).
func TestListRegion(t *testing.T) {
	tests := []struct {
		name         string
		explicit     []string
		configured   string
		want         string
		wantFallback bool
		wantMultiTip bool
	}{
		{name: "configured region is the default", configured: "us-west-2", want: "us-west-2"},
		{name: "--regions overrides the configured region", explicit: []string{"eu-west-1"}, configured: "us-west-2", want: "eu-west-1"},
		{name: "several --regions: the first", explicit: []string{"eu-west-1", "us-east-2"}, configured: "us-west-2", want: "eu-west-1", wantMultiTip: true},
		{name: "nothing configured falls back to us-east-1", want: "us-east-1", wantFallback: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := listRegion(&buf, tt.explicit, tt.configured); got != tt.want {
				t.Errorf("listRegion(%v, %q) = %q, want %q", tt.explicit, tt.configured, got, tt.want)
			}
			out := buf.String()
			if got := strings.Contains(out, "No default AWS region configured"); got != tt.wantFallback {
				t.Errorf("fallback note printed = %v, want %v; output %q", got, tt.wantFallback, out)
			}
			if got := strings.Contains(out, "'list' reports one region at a time"); got != tt.wantMultiTip {
				t.Errorf("one-region note printed = %v, want %v; output %q", got, tt.wantMultiTip, out)
			}
		})
	}
}

// TestListRegionFlagIsTheSharedOne pins the other half of #202: list has no
// local --region shadowing the root's, so `list --region X` goes through the
// root alias into the shared regions variable that listRegion reads.
func TestListRegionFlagIsTheSharedOne(t *testing.T) {
	if f := listCmd.LocalNonPersistentFlags().Lookup("region"); f != nil {
		t.Fatalf("list declares a local --region (default %q) that shadows the root --region alias", f.DefValue)
	}
	if listCmd.InheritedFlags().Lookup("region") == nil || listCmd.InheritedFlags().Lookup("regions") == nil {
		t.Fatal("list does not inherit the root --region / --regions flags")
	}

	saved := regions
	t.Cleanup(func() {
		regions = saved
		f := rootCmd.PersistentFlags().Lookup("region")
		_ = f.Value.(interface{ Replace([]string) error }).Replace([]string{})
		f.Changed = false
	})
	regions = []string{}
	if err := listCmd.ParseFlags([]string{"--region", "us-west-2"}); err != nil {
		t.Fatalf("parse list --region: %v", err)
	}
	if err := rootCmd.PersistentPreRunE(listCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE: %v", err)
	}
	if len(regions) != 1 || regions[0] != "us-west-2" {
		t.Fatalf("after `list --region us-west-2`, regions = %v, want [us-west-2]", regions)
	}
	if got := listRegion(&bytes.Buffer{}, regions, "eu-west-1"); got != "us-west-2" {
		t.Errorf("listRegion = %q, want us-west-2 (--region must beat the configured region)", got)
	}
}

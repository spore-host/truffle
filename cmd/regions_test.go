package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// fakeEnabledRegions stands in for (*aws.Client).GetEnabledRegions and records
// whether it was called, so tests can assert the single-region default skips
// the DescribeRegions round trip.
type fakeEnabledRegions struct {
	regions []string
	err     error
	calls   int
}

func (f *fakeEnabledRegions) list(context.Context) ([]string, error) {
	f.calls++
	return f.regions, f.err
}

var allEnabled = []string{"us-east-1", "us-west-2", "eu-west-1", "ap-southeast-2"}

// TestResolveRegions pins the configured-region default (find, search; #188): the configured
// region by default, --all-regions for every enabled region, --regions always
// winning, and a fallback to every enabled region when nothing is configured.
func TestResolveRegions(t *testing.T) {
	tests := []struct {
		name       string
		explicit   []string
		all        bool
		configured string
		want       []string
		wantScope  regionScope
		wantList   bool // whether GetEnabledRegions should be called
	}{
		{
			name:       "configured region is the default",
			configured: "us-west-2",
			want:       []string{"us-west-2"},
			wantScope:  scopeConfigured,
		},
		{
			name:       "--all-regions searches every enabled region",
			all:        true,
			configured: "us-west-2",
			want:       allEnabled,
			wantScope:  scopeAll,
			wantList:   true,
		},
		{
			name:       "--regions overrides the configured region",
			explicit:   []string{"eu-west-1", "us-east-1"},
			configured: "us-west-2",
			want:       []string{"eu-west-1", "us-east-1"},
			wantScope:  scopeExplicit,
		},
		{
			name:      "--regions with no configured region",
			explicit:  []string{"eu-west-1"},
			want:      []string{"eu-west-1"},
			wantScope: scopeExplicit,
		},
		{
			name:      "no configured region falls back to every enabled region",
			want:      allEnabled,
			wantScope: scopeAllFallback,
			wantList:  true,
		},
		{
			name:      "--all-regions with no configured region",
			all:       true,
			want:      allEnabled,
			wantScope: scopeAll,
			wantList:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEnabledRegions{regions: allEnabled}
			got, scope, err := resolveRegions(context.Background(), tt.explicit, tt.all, defaultConfiguredRegion, tt.configured, f.list)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("regions = %v, want %v", got, tt.want)
			}
			if scope != tt.wantScope {
				t.Errorf("scope = %v, want %v", scope, tt.wantScope)
			}
			if called := f.calls > 0; called != tt.wantList {
				t.Errorf("GetEnabledRegions called = %v, want %v", called, tt.wantList)
			}
		})
	}
}

// TestResolveRegions_Conflict: --regions and --all-regions ask for
// contradictory scopes; reject rather than silently picking one.
func TestResolveRegions_Conflict(t *testing.T) {
	for _, def := range []regionDefault{defaultConfiguredRegion, defaultAllRegions} {
		f := &fakeEnabledRegions{regions: allEnabled}
		_, _, err := resolveRegions(context.Background(), []string{"us-east-1"}, true, def, "us-west-2", f.list)
		if !errors.Is(err, errRegionsConflict) {
			t.Errorf("default %v: err = %v, want errRegionsConflict", def, err)
		}
	}
}

// TestResolveRegions_ListError: a failed enabled-region lookup must
// surface as an error, not an empty (silently-successful) search.
func TestResolveRegions_ListError(t *testing.T) {
	f := &fakeEnabledRegions{err: errors.New("AccessDenied")}
	for _, def := range []regionDefault{defaultConfiguredRegion, defaultAllRegions} {
		for _, all := range []bool{true, false} {
			if _, _, err := resolveRegions(context.Background(), nil, all, def, "", f.list); err == nil {
				t.Errorf("default %v, all=%v: expected error from failed region listing, got nil", def, all)
			}
		}
	}
}

// TestPrintRegionScope checks the scope notes point users at the right
// flags for each scope.
func TestPrintRegionScope(t *testing.T) {
	prev := outputFormat
	defer func() { outputFormat = prev }()
	outputFormat = "table"

	cases := []struct {
		scope   regionScope
		regions []string
		want    []string
	}{
		{scopeConfigured, []string{"us-west-2"}, []string{"us-west-2", "--all-regions"}},
		{scopeAll, make([]string, 18), []string{"all 18 enabled regions", "omit --all-regions"}},
		{scopeAllFallback, make([]string, 18), []string{"No default AWS region configured", "AWS_REGION", "--regions"}},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		printRegionScope(&buf, defaultConfiguredRegion, c.scope, c.regions)
		for _, w := range c.want {
			if !strings.Contains(buf.String(), w) {
				t.Errorf("scope %v: output %q missing %q", c.scope, buf.String(), w)
			}
		}
	}

	// Explicit --regions prints nothing; non-table output never prints.
	var buf bytes.Buffer
	printRegionScope(&buf, defaultConfiguredRegion, scopeExplicit, []string{"us-east-1"})
	outputFormat = "json"
	printRegionScope(&buf, defaultConfiguredRegion, scopeConfigured, []string{"us-east-1"})
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

// TestResolveRegions_AllDefault pins the every-region default used by spot,
// az, capacity and capacity-blocks (#194): every enabled region even when a
// region is configured, --all-regions is the same thing said explicitly, and
// --regions still wins.
func TestResolveRegions_AllDefault(t *testing.T) {
	tests := []struct {
		name       string
		explicit   []string
		all        bool
		configured string
		want       []string
		wantScope  regionScope
		wantList   bool
	}{
		{"configured region does not narrow the default", nil, false, "us-west-2", allEnabled, scopeAllDefault, true},
		{"no configured region", nil, false, "", allEnabled, scopeAllDefault, true},
		{"--all-regions", nil, true, "us-west-2", allEnabled, scopeAll, true},
		{"--regions wins", []string{"eu-west-1"}, false, "us-west-2", []string{"eu-west-1"}, scopeExplicit, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEnabledRegions{regions: allEnabled}
			got, scope, err := resolveRegions(context.Background(), tt.explicit, tt.all, defaultAllRegions, tt.configured, f.list)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("regions = %v, want %v", got, tt.want)
			}
			if scope != tt.wantScope {
				t.Errorf("scope = %v, want %v", scope, tt.wantScope)
			}
			if called := f.calls > 0; called != tt.wantList {
				t.Errorf("GetEnabledRegions called = %v, want %v", called, tt.wantList)
			}
		})
	}
}

// TestPrintRegionScope_AllDefaultIsQuiet: commands that default to every
// region never printed a scope note, and the find-style tips ("omit
// --all-regions to search only your configured region") would be wrong there.
func TestPrintRegionScope_AllDefaultIsQuiet(t *testing.T) {
	prev := outputFormat
	defer func() { outputFormat = prev }()
	outputFormat = "table"
	var buf bytes.Buffer
	for _, scope := range []regionScope{scopeAll, scopeAllDefault, scopeExplicit} {
		printRegionScope(&buf, defaultAllRegions, scope, make([]string, 18))
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for all-regions-default commands, got %q", buf.String())
	}
}

// fakeRegionClient is a regionClient for commandRegions tests.
type fakeRegionClient struct {
	configured string
	fakeEnabledRegions
}

func (c *fakeRegionClient) ConfiguredRegion() string { return c.configured }
func (c *fakeRegionClient) GetEnabledRegions(ctx context.Context) ([]string, error) {
	return c.list(ctx)
}

// TestCommandRegions_ReadsSharedFlags checks commandRegions wires the shared
// --regions and --all-regions flag variables through to the resolver.
func TestCommandRegions_ReadsSharedFlags(t *testing.T) {
	prevRegions, prevAll, prevOut := regions, allRegions, outputFormat
	defer func() { regions, allRegions, outputFormat = prevRegions, prevAll, prevOut }()
	outputFormat = "json" // keep stderr quiet

	c := &fakeRegionClient{configured: "us-west-2", fakeEnabledRegions: fakeEnabledRegions{regions: allEnabled}}

	regions, allRegions = nil, false
	if got, _ := commandRegions(context.Background(), c, defaultConfiguredRegion); !reflect.DeepEqual(got, []string{"us-west-2"}) {
		t.Errorf("configured default: got %v", got)
	}
	if got, _ := commandRegions(context.Background(), c, defaultAllRegions); !reflect.DeepEqual(got, allEnabled) {
		t.Errorf("all default: got %v", got)
	}

	allRegions = true
	if got, _ := commandRegions(context.Background(), c, defaultConfiguredRegion); !reflect.DeepEqual(got, allEnabled) {
		t.Errorf("--all-regions: got %v", got)
	}

	regions = []string{"eu-west-1"}
	if _, err := commandRegions(context.Background(), c, defaultAllRegions); !errors.Is(err, errRegionsConflict) {
		t.Errorf("--regions + --all-regions: err = %v, want errRegionsConflict", err)
	}
}

// TestRegionDefaults_PerCommand pins each multi-region command's default
// scope (#194) and checks every one registers --all-regions with help text
// that states that default. Changing a command's default is a user-visible
// behaviour change; this test makes it a deliberate one.
func TestRegionDefaults_PerCommand(t *testing.T) {
	cases := []struct {
		cmd  *cobra.Command
		got  regionDefault
		want regionDefault
	}{
		{findCmd, findRegionDefault, defaultConfiguredRegion},
		{searchCmd, searchRegionDefault, defaultConfiguredRegion},
		{spotCmd, spotRegionDefault, defaultAllRegions},
		{azCmd, azRegionDefault, defaultAllRegions},
		{capacityCmd, capacityRegionDefault, defaultAllRegions},
		{capacityBlocksCmd, capacityBlocksRegionDefault, defaultAllRegions},
	}
	for _, c := range cases {
		name := c.cmd.Name()
		if c.got != c.want {
			t.Errorf("%s: region default = %v, want %v", name, c.got, c.want)
		}
		f := c.cmd.Flags().Lookup("all-regions")
		if f == nil {
			t.Errorf("%s: missing --all-regions flag", name)
			continue
		}
		wantUsage := "default: only your configured region"
		if c.want == defaultAllRegions {
			wantUsage = "already the default"
		}
		if !strings.Contains(f.Usage, wantUsage) {
			t.Errorf("%s: --all-regions usage %q does not state the default (want %q)", name, f.Usage, wantUsage)
		}
	}
}

// TestRunAZ_AllRegionsConflictsWithAZ: --az already picks regions, so adding
// --all-regions is contradictory and must be rejected before any AWS call.
func TestRunAZ_AllRegionsConflictsWithAZ(t *testing.T) {
	prevAZ, prevAll := azFilter, allRegions
	defer func() { azFilter, allRegions = prevAZ, prevAll }()
	azFilter, allRegions = []string{"us-east-1a"}, true
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	if err := runAZSearch(azCmd, []string{"m7i.large"}); !errors.Is(err, errAZAllRegionsConflict) {
		t.Errorf("err = %v, want errAZAllRegionsConflict", err)
	}
}

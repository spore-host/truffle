package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
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

// TestResolveFindRegions pins find's region scope (#188): the configured
// region by default, --all-regions for every enabled region, --regions always
// winning, and a fallback to every enabled region when nothing is configured.
func TestResolveFindRegions(t *testing.T) {
	tests := []struct {
		name       string
		explicit   []string
		all        bool
		configured string
		want       []string
		wantScope  findRegionScope
		wantList   bool // whether GetEnabledRegions should be called
	}{
		{
			name:       "configured region is the default",
			configured: "us-west-2",
			want:       []string{"us-west-2"},
			wantScope:  findScopeConfigured,
		},
		{
			name:       "--all-regions searches every enabled region",
			all:        true,
			configured: "us-west-2",
			want:       allEnabled,
			wantScope:  findScopeAll,
			wantList:   true,
		},
		{
			name:       "--regions overrides the configured region",
			explicit:   []string{"eu-west-1", "us-east-1"},
			configured: "us-west-2",
			want:       []string{"eu-west-1", "us-east-1"},
			wantScope:  findScopeExplicit,
		},
		{
			name:      "--regions with no configured region",
			explicit:  []string{"eu-west-1"},
			want:      []string{"eu-west-1"},
			wantScope: findScopeExplicit,
		},
		{
			name:      "no configured region falls back to every enabled region",
			want:      allEnabled,
			wantScope: findScopeAllFallback,
			wantList:  true,
		},
		{
			name:      "--all-regions with no configured region",
			all:       true,
			want:      allEnabled,
			wantScope: findScopeAll,
			wantList:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeEnabledRegions{regions: allEnabled}
			got, scope, err := resolveFindRegions(context.Background(), tt.explicit, tt.all, tt.configured, f.list)
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

// TestResolveFindRegions_Conflict: --regions and --all-regions ask for
// contradictory scopes; reject rather than silently picking one.
func TestResolveFindRegions_Conflict(t *testing.T) {
	f := &fakeEnabledRegions{regions: allEnabled}
	_, _, err := resolveFindRegions(context.Background(), []string{"us-east-1"}, true, "us-west-2", f.list)
	if !errors.Is(err, errFindRegionsConflict) {
		t.Fatalf("err = %v, want errFindRegionsConflict", err)
	}
}

// TestResolveFindRegions_ListError: a failed enabled-region lookup must
// surface as an error, not an empty (silently-successful) search.
func TestResolveFindRegions_ListError(t *testing.T) {
	f := &fakeEnabledRegions{err: errors.New("AccessDenied")}
	for _, all := range []bool{true, false} {
		if _, _, err := resolveFindRegions(context.Background(), nil, all, "", f.list); err == nil {
			t.Errorf("all=%v: expected error from failed region listing, got nil", all)
		}
	}
}

// TestPrintFindRegionScope checks the scope notes point users at the right
// flags for each scope.
func TestPrintFindRegionScope(t *testing.T) {
	prev := outputFormat
	defer func() { outputFormat = prev }()
	outputFormat = "table"

	cases := []struct {
		scope   findRegionScope
		regions []string
		want    []string
	}{
		{findScopeConfigured, []string{"us-west-2"}, []string{"us-west-2", "--all-regions"}},
		{findScopeAll, make([]string, 18), []string{"all 18 enabled regions", "omit --all-regions"}},
		{findScopeAllFallback, make([]string, 18), []string{"No default AWS region configured", "AWS_REGION", "--regions"}},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		printFindRegionScope(&buf, c.scope, c.regions)
		for _, w := range c.want {
			if !strings.Contains(buf.String(), w) {
				t.Errorf("scope %v: output %q missing %q", c.scope, buf.String(), w)
			}
		}
	}

	// Explicit --regions prints nothing; non-table output never prints.
	var buf bytes.Buffer
	printFindRegionScope(&buf, findScopeExplicit, []string{"us-east-1"})
	outputFormat = "json"
	printFindRegionScope(&buf, findScopeConfigured, []string{"us-east-1"})
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}
}

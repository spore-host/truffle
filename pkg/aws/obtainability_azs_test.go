package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestObtainability_OfferedAZLookupFailureIsUnknown is the end-to-end #274 gate:
// when DescribeInstanceTypeOfferings fails, Obtainability records the offered-AZ
// signal as unavailable (carried in JSON as offered_azs_state), leaves
// OfferedAZs empty-because-unknown rather than zero-because-absent, and keeps the
// warning. Before #274 nothing distinguished this from a type offered nowhere,
// and `truffle available` printed "not offered in this region".
func TestObtainability_OfferedAZLookupFailureIsUnknown(t *testing.T) {
	c := newCapBlockErrorClient(t, &capBlockErrorTransport{
		action: "DescribeInstanceTypeOfferings",
		code:   "RequestLimitExceeded", status: http.StatusServiceUnavailable,
		message: "Request limit exceeded.",
	})
	obt, err := c.Obtainability(context.Background(), "m7i.large", "us-east-1")
	if err != nil {
		t.Fatalf("Obtainability: %v", err)
	}
	if obt.OfferedAZsState != SignalUnavailable {
		t.Errorf("OfferedAZsState = %q, want %q", obt.OfferedAZsState, SignalUnavailable)
	}
	if obt.OfferedAZsKnown() {
		t.Error("OfferedAZsKnown() = true after a failed lookup")
	}
	if !hasWarningAbout(obt.Warnings, "offered azs unavailable") {
		t.Errorf("failed lookup left no warning; warnings = %v", obt.Warnings)
	}
	raw, err := json.Marshal(obt)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"offered_azs_state":"unavailable"`; !strings.Contains(string(raw), want) {
		t.Errorf("JSON lacks %s: %s", want, raw)
	}
}

// TestRecordOfferedAZs pins the state transitions of the offered-AZ signal: a
// failure is unavailable with a warning and no list; a successful lookup is
// collected — including an empty one, which is the only case that may render as
// "not offered" (#274).
func TestRecordOfferedAZs(t *testing.T) {
	tests := []struct {
		name        string
		offered     []string
		err         error
		wantState   SignalState
		wantKnown   bool
		wantWarning bool
		wantAZs     []string
	}{
		{name: "lookup failed", offered: []string{"us-east-1a"}, err: errors.New("api error AccessDenied"),
			wantState: SignalUnavailable, wantWarning: true},
		{name: "offered nowhere", offered: nil, wantState: SignalCollected, wantKnown: true, wantAZs: []string{}},
		{name: "deduplicated", offered: []string{"us-east-1b", "us-east-1a", "us-east-1b"},
			wantState: SignalCollected, wantKnown: true, wantAZs: []string{"us-east-1a", "us-east-1b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Obtainability{}
			warning := o.recordOfferedAZs(tt.offered, tt.err)
			if (warning != "") != tt.wantWarning {
				t.Errorf("warning = %q, want warning: %v", warning, tt.wantWarning)
			}
			if tt.wantWarning && !strings.Contains(warning, "AccessDenied") {
				t.Errorf("cause dropped from the warning: %q", warning)
			}
			if o.OfferedAZsState != tt.wantState {
				t.Errorf("state = %q, want %q", o.OfferedAZsState, tt.wantState)
			}
			if o.OfferedAZsKnown() != tt.wantKnown {
				t.Errorf("OfferedAZsKnown() = %v, want %v", o.OfferedAZsKnown(), tt.wantKnown)
			}
			if strings.Join(o.OfferedAZs, ",") != strings.Join(tt.wantAZs, ",") {
				t.Errorf("OfferedAZs = %v, want %v", o.OfferedAZs, tt.wantAZs)
			}
		})
	}
}

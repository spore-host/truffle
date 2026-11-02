package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/spore-host/truffle/pkg/quotas"
	"github.com/spore-host/truffle/pkg/testutil"
)

// quotaDeniedTransport answers Service Quotas GetServiceQuota for one quota code
// with AccessDeniedException — a missing servicequotas:GetServiceQuota grant —
// and passes everything else through to the Substrate emulator.
type quotaDeniedTransport struct {
	base http.RoundTripper
	code string
}

func (t *quotaDeniedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.Header.Get("X-Amz-Target"), ".GetServiceQuota") && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"`+t.code+`"`)) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/x-amz-json-1.1"}},
				Body: io.NopCloser(strings.NewReader(`{"__type":"AccessDeniedException",` +
					`"message":"not authorized to perform: servicequotas:GetServiceQuota"}`)),
				Request: req,
			}, nil
		}
	}
	return t.base.RoundTrip(req)
}

// TestObtainability_UnreadableQuotaIsUnknown is the end-to-end #271 gate.
// GetServiceQuota for the X-family On-Demand quota is denied, so GetQuotas
// records that family's read as failed (#167) without returning an error.
// Obtainability must report the headroom as unknown (nil, quota_headroom_state
// "unavailable", a warning naming the quota), never as 0. Before #271 the
// one-value map read turned the missing key into a limit of 0 and `available`
// printed "… is 0".
//
// An X type also pins the #167 single source of truth: its quota code comes
// from quotas.QuotaCodeFor, not a local 3-entry map that left it blank.
func TestObtainability_UnreadableQuotaIsUnknown(t *testing.T) {
	wantCode, _ := quotas.QuotaCodeFor(quotas.FamilyX, false)
	env := testutil.SubstrateServer(t)
	cfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithBaseEndpoint(env.URL),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "test")),
		awsconfig.WithHTTPClient(&http.Client{Transport: &quotaDeniedTransport{base: http.DefaultTransport, code: wantCode}}),
		awsconfig.WithRetryMaxAttempts(1),
	)
	if err != nil {
		t.Fatalf("build AWS config: %v", err)
	}
	c := NewClientFromConfig(cfg)
	obt, err := c.Obtainability(context.Background(), "x2idn.16xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("Obtainability: %v", err)
	}
	if obt.OnDemandQuotaHeadroom != nil {
		t.Errorf("OnDemandQuotaHeadroom = %d, want nil: the quota was never read", *obt.OnDemandQuotaHeadroom)
	}
	if obt.QuotaHeadroomState != SignalUnavailable {
		t.Errorf("QuotaHeadroomState = %q, want %q", obt.QuotaHeadroomState, SignalUnavailable)
	}
	if obt.QuotaCode != wantCode || wantCode == "" {
		t.Errorf("QuotaCode = %q, want %q (quotas.QuotaCodeFor)", obt.QuotaCode, wantCode)
	}
	var quotaWarning string
	for _, w := range obt.Warnings {
		if strings.HasPrefix(w, "quota headroom unavailable") {
			quotaWarning = w
		}
	}
	if quotaWarning == "" || !strings.Contains(quotaWarning, wantCode) ||
		!strings.Contains(quotaWarning, "not zero") || !strings.Contains(quotaWarning, "AccessDenied") {
		t.Errorf("no warning naming the unread quota %s; warnings = %v", wantCode, obt.Warnings)
	}
	raw, err := json.Marshal(obt)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"quota_headroom_state":"unavailable"`; !strings.Contains(string(raw), want) {
		t.Errorf("JSON lacks %s: %s", want, raw)
	}
	if strings.Contains(string(raw), `"on_demand_quota_headroom"`) {
		t.Errorf("JSON carries a headroom for an unread quota: %s", raw)
	}
}

// TestRecordQuotaHeadroom pins the quota signal's states: a failed GetQuotas or
// a family missing from OnDemand (with or without a recorded cause) is
// unavailable with a warning and no value; a quota that was read — including a
// genuine zero — is collected (#271).
func TestRecordQuotaHeadroom(t *testing.T) {
	denied := errors.New("api error AccessDeniedException: not authorized to perform servicequotas:GetServiceQuota")
	tests := []struct {
		name         string
		info         *quotas.QuotaInfo
		err          error
		wantState    SignalState
		wantHeadroom *int
		wantInWarn   string
	}{
		{name: "GetQuotas failed", err: errors.New("failed to get current usage: throttled"),
			wantState: SignalUnavailable, wantInWarn: "throttled"},
		{name: "family read failed", info: &quotas.QuotaInfo{
			OnDemand:       map[quotas.QuotaFamily]int32{"Standard": 640},
			OnDemandErrors: map[quotas.QuotaFamily]error{"P": denied},
		}, wantState: SignalUnavailable, wantInWarn: "AccessDeniedException"},
		{name: "family absent, no cause recorded", info: &quotas.QuotaInfo{
			OnDemand: map[quotas.QuotaFamily]int32{},
		}, wantState: SignalUnavailable, wantInWarn: "not zero"},
		{name: "genuine zero quota", info: &quotas.QuotaInfo{
			OnDemand: map[quotas.QuotaFamily]int32{"P": 0},
		}, wantState: SignalCollected, wantHeadroom: intPtrAWS(0)},
		{name: "usage subtracted, floored", info: &quotas.QuotaInfo{
			OnDemand: map[quotas.QuotaFamily]int32{"P": 192},
			Usage:    map[quotas.QuotaFamily]int32{"P": 384},
		}, wantState: SignalCollected, wantHeadroom: intPtrAWS(0)},
		{name: "headroom", info: &quotas.QuotaInfo{
			OnDemand: map[quotas.QuotaFamily]int32{"P": 384},
			Usage:    map[quotas.QuotaFamily]int32{"P": 192},
		}, wantState: SignalCollected, wantHeadroom: intPtrAWS(192)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Obtainability{QuotaFamily: "P", QuotaCode: "L-417A185B"}
			warning := o.recordQuotaHeadroom(tt.info, "P", tt.err)
			if (warning != "") != (tt.wantInWarn != "") || !strings.Contains(warning, tt.wantInWarn) {
				t.Errorf("warning = %q, want one containing %q", warning, tt.wantInWarn)
			}
			if o.QuotaHeadroomState != tt.wantState {
				t.Errorf("state = %q, want %q", o.QuotaHeadroomState, tt.wantState)
			}
			if o.QuotaHeadroomKnown() != (tt.wantState == SignalCollected) {
				t.Errorf("QuotaHeadroomKnown() = %v in state %q", o.QuotaHeadroomKnown(), o.QuotaHeadroomState)
			}
			switch {
			case tt.wantHeadroom == nil && o.OnDemandQuotaHeadroom != nil:
				t.Errorf("headroom = %d, want nil (unknown, not zero)", *o.OnDemandQuotaHeadroom)
			case tt.wantHeadroom != nil && (o.OnDemandQuotaHeadroom == nil || *o.OnDemandQuotaHeadroom != *tt.wantHeadroom):
				t.Errorf("headroom = %v, want %d", o.OnDemandQuotaHeadroom, *tt.wantHeadroom)
			}
		})
	}
}

func intPtrAWS(n int) *int { return &n }

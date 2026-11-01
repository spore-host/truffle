package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/spore-host/truffle/pkg/testutil"
)

// capBlockErrorTransport answers every DescribeCapacityBlockOfferings call with
// a canned EC2 error and passes everything else through to base (the Substrate
// emulator), so Obtainability runs end to end against a chosen Capacity Block
// outcome.
type capBlockErrorTransport struct {
	base    http.RoundTripper
	code    string
	message string
	status  int
}

func (t *capBlockErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte("Action=DescribeCapacityBlockOfferings")) {
			xml := `<?xml version="1.0" encoding="UTF-8"?><Response><Errors><Error><Code>` + t.code +
				`</Code><Message>` + t.message + `</Message></Error></Errors><RequestID>test</RequestID></Response>`
			return &http.Response{
				StatusCode: t.status,
				Header:     http.Header{"Content-Type": []string{"text/xml"}},
				Body:       io.NopCloser(strings.NewReader(xml)),
				Request:    req,
			}, nil
		}
	}
	return t.base.RoundTrip(req)
}

func newCapBlockErrorClient(t *testing.T, tr *capBlockErrorTransport) *Client {
	t.Helper()
	env := testutil.SubstrateServer(t)
	tr.base = http.DefaultTransport
	cfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithBaseEndpoint(env.URL),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "test")),
		awsconfig.WithHTTPClient(&http.Client{Transport: tr}),
		awsconfig.WithRetryMaxAttempts(1),
	)
	if err != nil {
		t.Fatalf("build AWS config: %v", err)
	}
	return NewClientFromConfig(cfg)
}

// TestObtainability_CapacityBlockIneligibleIsAState is the end-to-end #272
// gate: when AWS says the type isn't a Capacity Block type, Obtainability
// records the ineligible state (carried in JSON) and NO capacity-block warning,
// while a real failure of the same call is still warned.
func TestObtainability_CapacityBlockIneligibleIsAState(t *testing.T) {
	tests := []struct {
		name        string
		tr          *capBlockErrorTransport
		wantState   CapacityBlockState
		wantWarning bool
	}{
		{
			name: "ineligible type",
			tr: &capBlockErrorTransport{code: "InvalidParameterValue", status: http.StatusBadRequest,
				message: "The instance type 'c7i.4xlarge' is not supported for Capacity Blocks in 'us-east-1'. Specify a compatible instance type and try again."},
			wantState: CapacityBlockIneligible,
		},
		{
			name: "access denied",
			tr: &capBlockErrorTransport{code: "UnauthorizedOperation", status: http.StatusForbidden,
				message: "You are not authorized to perform this operation."},
			wantState:   CapacityBlockUnavailable,
			wantWarning: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCapBlockErrorClient(t, tt.tr)
			obt, err := c.Obtainability(context.Background(), "c7i.4xlarge", "us-east-1")
			if err != nil {
				t.Fatalf("Obtainability: %v", err)
			}
			if got := hasWarningAbout(obt.Warnings, "capacity block"); got != tt.wantWarning {
				t.Errorf("capacity-block warning present = %v, want %v; warnings = %v", got, tt.wantWarning, obt.Warnings)
			}
			if obt.CapacityBlockState != tt.wantState {
				t.Errorf("CapacityBlockState = %q, want %q", obt.CapacityBlockState, tt.wantState)
			}
			if obt.CapacityBlockOfferings != nil {
				t.Errorf("CapacityBlockOfferings = %d, want nil (no count was obtained)", *obt.CapacityBlockOfferings)
			}
			raw, err := json.Marshal(obt)
			if err != nil {
				t.Fatal(err)
			}
			if want := `"capacity_block_state":"` + string(tt.wantState) + `"`; !strings.Contains(string(raw), want) {
				t.Errorf("JSON lacks %s: %s", want, raw)
			}
		})
	}
}

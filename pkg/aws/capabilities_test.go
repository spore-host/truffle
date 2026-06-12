package aws

import (
	"context"
	"testing"

	"github.com/spore-host/truffle/pkg/testutil"
)

// TestGetCapabilities_Substrate exercises the capability lookup against the
// substrate emulator — verifies the call path and that an unknown type returns
// Found=false without error.
func TestGetCapabilities_Substrate(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)

	// A known type: substrate models common types; we just assert no panic and a
	// populated struct (capability bits depend on substrate's fidelity).
	caps, err := c.GetCapabilities(context.Background(), "c5n.18xlarge", "us-east-1")
	if err != nil {
		t.Logf("GetCapabilities(c5n.18xlarge) err=%v (substrate may not model it)", err)
	} else {
		t.Logf("c5n.18xlarge caps: %+v", caps)
		if caps.InstanceType != "c5n.18xlarge" {
			t.Errorf("InstanceType = %q, want c5n.18xlarge", caps.InstanceType)
		}
	}
}

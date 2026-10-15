package aws

import (
	"testing"

	"github.com/spore-host/truffle/pkg/metadata"
)

// TestLiveFamiliesAreRecognised: every family EC2 offers is a word find
// recognises, from the catalog snapshot or the Nitro table (#229).
func TestLiveFamiliesAreRecognised(t *testing.T) {
	missing := map[string]bool{}
	for _, it := range liveCatalog(t) {
		family := InstanceFamily(string(it.InstanceType))
		if !metadata.IsInstanceFamily(family) && NitroGeneration(family) == NitroGenerationUnknown {
			missing[family] = true
		}
	}
	for family := range missing {
		t.Errorf("family %s is not recognised as a query word", family)
	}
}

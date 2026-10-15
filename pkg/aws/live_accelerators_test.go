package aws

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spore-host/truffle/pkg/metadata"
)

// hasAccelerator reports whether an instance type carries a GPU, inference
// accelerator, Neuron device, FPGA or media accelerator.
func hasAccelerator(it types.InstanceTypeInfo) bool {
	return (it.GpuInfo != nil && len(it.GpuInfo.Gpus) > 0) ||
		(it.InferenceAcceleratorInfo != nil && len(it.InferenceAcceleratorInfo.Accelerators) > 0) ||
		(it.NeuronInfo != nil && len(it.NeuronInfo.NeuronDevices) > 0) ||
		(it.FpgaInfo != nil && len(it.FpgaInfo.Fpgas) > 0) ||
		(it.MediaAcceleratorInfo != nil && len(it.MediaAcceleratorInfo.Accelerators) > 0)
}

// TestLiveAcceleratorFamilies: the recorded accelerator families, which CPU
// words leave out unless the query names a GPU (#228), match the live
// catalog's GpuInfo/InferenceAcceleratorInfo/NeuronInfo/FpgaInfo/
// MediaAcceleratorInfo, in both directions.
func TestLiveAcceleratorFamilies(t *testing.T) {
	live := make(map[string]bool) // family -> any type has an accelerator
	for _, it := range liveCatalog(t) {
		name := string(it.InstanceType)
		family := name[:strings.IndexByte(name, '.')]
		live[family] = live[family] || hasAccelerator(it)
	}
	for family, accel := range live {
		if accel != metadata.IsAcceleratorFamily(family) {
			t.Errorf("%s: live accelerator = %v, recorded = %v", family, accel, metadata.IsAcceleratorFamily(family))
		}
	}
}

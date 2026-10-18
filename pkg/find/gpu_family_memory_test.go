package find

import (
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

// TestExplainMatch_GPUMemoryPerFamily checks the "why it matched" line
// quotes the GPU memory of the family the type is in, where a model's
// memory varies (#240): V100 16 GB on p3 and 32 GB on p3dn, A100 40 GB on
// p4d and 80 GB on p4de. Trainium2 is its documented 96 GB per chip (EC2's
// trn2 record says 512 GiB per device; #242).
func TestExplainMatch_GPUMemoryPerFamily(t *testing.T) {
	for _, tt := range []struct{ query, it, want string }{
		{"v100", "p3.2xlarge", "GPU: V100 (16 GiB"},
		{"v100", "p3dn.24xlarge", "GPU: V100 (32 GiB"},
		{"a100", "p4d.24xlarge", "GPU: A100 (40 GiB"},
		{"a100", "p4de.24xlarge", "GPU: A100 (80 GiB"},
		{"inferentia2", "inf2.xlarge", "GPU: Inferentia2 (32 GiB"},
		{"trainium2", "trn2.48xlarge", "GPU: Trainium2 (96 GiB"},
		{"h200", "p5en.48xlarge", "GPU: H200 (141 GiB"},
	} {
		got := strings.Join(ExplainMatch(aws.InstanceTypeResult{InstanceType: tt.it}, mustParse(t, tt.query)), "; ")
		if !strings.Contains(got, tt.want) {
			t.Errorf("%q on %s: reasons %q, want %q", tt.query, tt.it, got, tt.want)
		}
	}
}

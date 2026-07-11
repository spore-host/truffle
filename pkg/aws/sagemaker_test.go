package aws

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/quotas"
	"github.com/spore-host/truffle/pkg/testutil"
)

// fakeSMLister is a deterministic SageMakerTypeLister for tests: it returns a
// fixed offered set per region without touching the Service Quotas API. The
// per-type list carries only the type name; spotEligible and trainingQuota
// (both keyed by ml.* type) supply optional quota detail for tests that need it.
type fakeSMLister struct {
	byRegion      map[string][]string
	spotEligible  map[string]bool
	trainingQuota map[string]float64
	err           error
}

func (f fakeSMLister) OfferedTypes(_ context.Context, region string) ([]quotas.SageMakerTypeQuota, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []quotas.SageMakerTypeQuota
	for _, t := range f.byRegion[region] {
		q := quotas.SageMakerTypeQuota{InstanceType: t, TrainingJobLimit: -1}
		q.ManagedSpotEligible = f.spotEligible[t]
		if v, ok := f.trainingQuota[t]; ok {
			q.TrainingJobLimit = v
		}
		out = append(out, q)
	}
	return out, nil
}

// resultByType finds a result by instance type, or nil.
func resultByType(results []InstanceTypeResult, t string) *InstanceTypeResult {
	for i := range results {
		if results[i].InstanceType == t {
			return &results[i]
		}
	}
	return nil
}

// TestSearchSageMaker_MapsEC2Specs verifies that an ml.* type's specs are
// derived from the underlying EC2 type (ml.g4dn.xlarge → g4dn.xlarge), the row
// is tagged Service=sagemaker, SpawnSupported=false, and price is left 0
// (SageMaker pricing is issue #80, out of scope for discovery).
func TestSearchSageMaker_MapsEC2Specs(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{byRegion: map[string][]string{
		"us-east-1": {"ml.g4dn.xlarge", "ml.c5.xlarge"},
	}})

	// Match everything so both offered types come back.
	matcher := regexp.MustCompile(".*")
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchSageMakerInstanceTypes() error = %v", err)
	}

	gpu := resultByType(results, "ml.g4dn.xlarge")
	if gpu == nil {
		t.Fatalf("ml.g4dn.xlarge not in results: %+v", results)
	}
	if gpu.Service != "sagemaker" {
		t.Errorf("Service = %q, want %q", gpu.Service, "sagemaker")
	}
	if gpu.SpawnSupported {
		t.Error("SpawnSupported should be false for SageMaker results")
	}
	if gpu.OnDemandPrice != 0 {
		t.Errorf("OnDemandPrice = %v, want 0 (pricing is out of scope, #80)", gpu.OnDemandPrice)
	}
	if gpu.VCPUs == 0 || gpu.MemoryMiB == 0 {
		t.Errorf("specs not enriched from EC2 peer: vcpus=%d mem=%d", gpu.VCPUs, gpu.MemoryMiB)
	}
	if gpu.GPUs == 0 {
		t.Errorf("g4dn.xlarge should map a GPU, got GPUs=%d", gpu.GPUs)
	}
	if gpu.InstanceFamily != "ml.g4dn" {
		t.Errorf("InstanceFamily = %q, want %q", gpu.InstanceFamily, "ml.g4dn")
	}

	cpu := resultByType(results, "ml.c5.xlarge")
	if cpu == nil {
		t.Fatalf("ml.c5.xlarge not in results: %+v", results)
	}
	if cpu.GPUs != 0 {
		t.Errorf("c5.xlarge should have no GPU, got %d", cpu.GPUs)
	}
}

// TestSearchSageMaker_FoldsQuotaDetail verifies managed-spot eligibility and the
// training-job quota are folded into results from the quota data (#81).
func TestSearchSageMaker_FoldsQuotaDetail(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{
		byRegion:      map[string][]string{"us-east-1": {"ml.g4dn.xlarge", "ml.c5.xlarge"}},
		spotEligible:  map[string]bool{"ml.g4dn.xlarge": true}, // c5 not eligible
		trainingQuota: map[string]float64{"ml.g4dn.xlarge": 2}, // c5 has no training quota
	})

	matcher := regexp.MustCompile(".*")
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchSageMakerInstanceTypes() error = %v", err)
	}

	gpu := resultByType(results, "ml.g4dn.xlarge")
	if gpu == nil {
		t.Fatalf("ml.g4dn.xlarge not in results: %+v", results)
	}
	if !gpu.ManagedSpotEligible {
		t.Error("ml.g4dn.xlarge should be managed-spot eligible")
	}
	if gpu.TrainingJobQuota == nil || *gpu.TrainingJobQuota != 2 {
		t.Errorf("ml.g4dn.xlarge TrainingJobQuota = %v, want 2", gpu.TrainingJobQuota)
	}

	cpu := resultByType(results, "ml.c5.xlarge")
	if cpu == nil {
		t.Fatalf("ml.c5.xlarge not in results: %+v", results)
	}
	if cpu.ManagedSpotEligible {
		t.Error("ml.c5.xlarge should NOT be managed-spot eligible")
	}
	if cpu.TrainingJobQuota != nil {
		t.Errorf("ml.c5.xlarge TrainingJobQuota = %v, want nil (no training quota)", cpu.TrainingJobQuota)
	}
}

// TestSearchSageMaker_PatternFilter verifies the pattern is matched against the
// ml.-prefixed name so "ml.c5.*" selects only the c5 family.
func TestSearchSageMaker_PatternFilter(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{byRegion: map[string][]string{
		"us-east-1": {"ml.g4dn.xlarge", "ml.c5.xlarge", "ml.c5.2xlarge"},
	}})

	matcher := regexp.MustCompile(`^ml\.c5\..*$`)
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchSageMakerInstanceTypes() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 c5 results, got %d: %+v", len(results), results)
	}
	for _, r := range results {
		if !strings.HasPrefix(r.InstanceType, "ml.c5.") {
			t.Errorf("unexpected type %q for pattern ml.c5.*", r.InstanceType)
		}
	}
}

// TestSearchSageMaker_MinVCPUFilter verifies spec filters (via the EC2 peer)
// apply to SageMaker results the same way they do for EC2.
func TestSearchSageMaker_MinVCPUFilter(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{byRegion: map[string][]string{
		// c5.xlarge = 4 vCPU, c5.2xlarge = 8 vCPU (Substrate seed).
		"us-east-1": {"ml.c5.xlarge", "ml.c5.2xlarge"},
	}})

	matcher := regexp.MustCompile(".*")
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{MinVCPUs: 8})
	if err != nil {
		t.Fatalf("SearchSageMakerInstanceTypes() error = %v", err)
	}
	for _, r := range results {
		if r.VCPUs < 8 {
			t.Errorf("result %s has %d vCPUs, want >= 8", r.InstanceType, r.VCPUs)
		}
	}
	if resultByType(results, "ml.c5.xlarge") != nil {
		t.Error("ml.c5.xlarge (4 vCPU) should have been filtered out by --min-vcpu 8")
	}
}

// TestSearchSageMaker_NoEC2Peer verifies that an offered ml.* type with no EC2
// counterpart still appears (availability is real even when specs are unknown),
// with zeroed specs, when no spec-based filters are requested.
func TestSearchSageMaker_NoEC2Peer(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{byRegion: map[string][]string{
		// ml.p6.128xlarge has no EC2 peer in the Substrate seed.
		"us-east-1": {"ml.p6.128xlarge"},
	}})

	matcher := regexp.MustCompile(".*")
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{})
	if err != nil {
		t.Fatalf("SearchSageMakerInstanceTypes() error = %v", err)
	}
	r := resultByType(results, "ml.p6.128xlarge")
	if r == nil {
		t.Fatalf("quota-only ml.p6.128xlarge should still be listed: %+v", results)
	}
	if r.Service != "sagemaker" || r.VCPUs != 0 {
		t.Errorf("quota-only row = %+v, want Service=sagemaker with zeroed specs", *r)
	}
}

// TestSearchSageMaker_NoEC2Peer_FilteredWhenSpecRequested verifies that a
// quota-only type is dropped when a spec filter is set, since its specs can't
// be verified.
func TestSearchSageMaker_NoEC2Peer_FilteredWhenSpecRequested(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{byRegion: map[string][]string{
		"us-east-1": {"ml.p6.128xlarge"},
	}})

	matcher := regexp.MustCompile(".*")
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1"}, matcher, FilterOptions{MinVCPUs: 8})
	if err != nil {
		t.Fatalf("SearchSageMakerInstanceTypes() error = %v", err)
	}
	if resultByType(results, "ml.p6.128xlarge") != nil {
		t.Error("quota-only type should be dropped when --min-vcpu is set (specs unverifiable)")
	}
}

// TestSearchSageMaker_AllRegionsFail verifies the #63 contract: when every
// region's offered-types lookup errors, the search returns an error rather than
// masquerading as an empty result.
func TestSearchSageMaker_AllRegionsFail(t *testing.T) {
	env := testutil.SubstrateServer(t)
	c := NewClientFromConfig(env.AWSConfig)
	c.SetSageMakerTypeLister(fakeSMLister{err: context.DeadlineExceeded})

	matcher := regexp.MustCompile(".*")
	results, err := c.SearchSageMakerInstanceTypes(context.Background(),
		[]string{"us-east-1", "us-west-2"}, matcher, FilterOptions{})
	if err == nil {
		t.Fatalf("expected an error when all regions fail, got nil (results=%d)", len(results))
	}
	if !strings.Contains(err.Error(), "region queries failed") {
		t.Errorf("error should explain the total failure, got: %v", err)
	}
}

// TestClient_DefaultSageMakerTypeLister verifies the client lazily installs the
// default Service Quotas-backed lister when none is injected.
func TestClient_DefaultSageMakerTypeLister(t *testing.T) {
	c := &Client{}
	if c.sageMakerTypeLister() == nil {
		t.Fatal("sageMakerTypeLister() returned nil default")
	}
}

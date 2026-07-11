package quotas

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// fakeQuotaLister returns a canned SageMaker quota list without hitting AWS.
type fakeQuotaLister struct {
	quotas []SageMakerQuota
	err    error
}

func (f fakeQuotaLister) ListSageMakerInstanceQuotas(_ context.Context, _ string) ([]SageMakerQuota, error) {
	return f.quotas, f.err
}

func TestOfferedSageMakerTypes(t *testing.T) {
	lister := fakeQuotaLister{quotas: []SageMakerQuota{
		{Name: "ml.g5.2xlarge for processing job usage"},
		{Name: "ml.g5.2xlarge for training job usage"}, // dup type, different job
		{Name: "ml.c5.xlarge for endpoint usage"},
		{Name: "ml.p4d.24xlarge"},           // bare, no " for " suffix
		{Name: "some non-ml quota"},         // ignored (no ml. prefix)
		{Name: "Total number of instances"}, // ignored
	}}

	got, err := OfferedSageMakerTypes(context.Background(), lister, "us-east-1")
	if err != nil {
		t.Fatalf("OfferedSageMakerTypes() error = %v", err)
	}
	want := []string{"ml.c5.xlarge", "ml.g5.2xlarge", "ml.p4d.24xlarge"} // deduped + sorted
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OfferedSageMakerTypes() = %v, want %v", got, want)
	}
}

func TestOfferedSageMakerTypes_PropagatesError(t *testing.T) {
	sentinel := errors.New("access denied")
	_, err := OfferedSageMakerTypes(context.Background(),
		fakeQuotaLister{err: sentinel}, "us-east-1")
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestOfferedSageMakerTypesDetailed(t *testing.T) {
	lister := fakeQuotaLister{quotas: []SageMakerQuota{
		{Name: "ml.g5.2xlarge for training job usage", Value: 4},
		{Name: "ml.g5.2xlarge for spot training job usage", Value: 0}, // spot-eligible
		{Name: "ml.g5.2xlarge for processing job usage", Value: 2},
		{Name: "ml.c5.xlarge for endpoint usage", Value: 1}, // no training, no spot
	}}

	got, err := OfferedSageMakerTypesDetailed(context.Background(), lister, "us-east-1")
	if err != nil {
		t.Fatalf("OfferedSageMakerTypesDetailed() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 types, got %d: %+v", len(got), got)
	}

	// Sorted: ml.c5.xlarge, ml.g5.2xlarge.
	c5, g5 := got[0], got[1]
	if c5.InstanceType != "ml.c5.xlarge" || g5.InstanceType != "ml.g5.2xlarge" {
		t.Fatalf("unexpected ordering: %q, %q", c5.InstanceType, g5.InstanceType)
	}

	// g5: training limit 4, spot-eligible (has a spot training quota row).
	if g5.TrainingJobLimit != 4 {
		t.Errorf("g5 TrainingJobLimit = %v, want 4", g5.TrainingJobLimit)
	}
	if !g5.ManagedSpotEligible {
		t.Error("g5 should be managed-spot eligible (has spot training quota)")
	}

	// c5: no training-job quota → -1 sentinel; not spot-eligible.
	if c5.TrainingJobLimit != -1 {
		t.Errorf("c5 TrainingJobLimit = %v, want -1 (no training quota)", c5.TrainingJobLimit)
	}
	if c5.ManagedSpotEligible {
		t.Error("c5 should NOT be managed-spot eligible (no spot training quota)")
	}
}

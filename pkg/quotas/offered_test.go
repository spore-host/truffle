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

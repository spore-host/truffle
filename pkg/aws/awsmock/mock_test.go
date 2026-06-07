package awsmock_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
	"github.com/spore-host/truffle/pkg/aws/awsmock"
)

func TestMockFinder_SearchInstanceTypes(t *testing.T) {
	m := awsmock.New(
		awsmock.WithRegions([]string{"us-east-1", "us-west-2"}),
		awsmock.WithInstances([]aws.InstanceTypeResult{
			{InstanceType: "m7i.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8192, Architecture: "x86_64"},
			{InstanceType: "m7i.xlarge", Region: "us-east-1", VCPUs: 4, MemoryMiB: 16384, Architecture: "x86_64"},
			{InstanceType: "c7g.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 4096, Architecture: "arm64"},
			{InstanceType: "m7i.large", Region: "us-west-2", VCPUs: 2, MemoryMiB: 8192, Architecture: "x86_64"},
		}),
	)

	ctx := context.Background()

	// Search all m7i in us-east-1
	matcher := regexp.MustCompile(`^m7i\..*$`)
	results, err := m.SearchInstanceTypes(ctx, []string{"us-east-1"}, matcher, aws.FilterOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Filter by architecture
	results, err = m.SearchInstanceTypes(ctx, []string{"us-east-1"}, nil, aws.FilterOptions{Architecture: "arm64"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].InstanceType != "c7g.large" {
		t.Errorf("expected c7g.large, got %v", results)
	}

	// Filter by min vCPUs
	results, err = m.SearchInstanceTypes(ctx, []string{"us-east-1"}, nil, aws.FilterOptions{MinVCPUs: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].InstanceType != "m7i.xlarge" {
		t.Errorf("expected m7i.xlarge, got %v", results)
	}
}

func TestMockFinder_OnDemandPrice(t *testing.T) {
	m := awsmock.New(
		awsmock.WithOnDemandPrices(map[string]float64{
			"m7i.large/us-east-1": 0.1008,
			"c7g.large/us-east-1": 0.0725,
		}),
	)

	ctx := context.Background()

	price, err := m.OnDemandPrice(ctx, "m7i.large", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != 0.1008 {
		t.Errorf("expected 0.1008, got %f", price)
	}

	_, err = m.OnDemandPrice(ctx, "unknown.large", "us-east-1")
	if err == nil {
		t.Error("expected error for unknown instance type")
	}
}

func TestMockFinder_HourlyRate(t *testing.T) {
	m := awsmock.New(
		awsmock.WithOnDemandPrices(map[string]float64{"m7i.large/us-east-1": 0.1008}),
		awsmock.WithSpotPrices([]aws.SpotPriceResult{
			{InstanceType: "m7i.large", Region: "us-east-1", SpotPrice: 0.04},
		}),
	)

	ctx := context.Background()

	rate, err := m.HourlyRate(ctx, "m7i.large", "us-east-1", "on-demand")
	if err != nil || rate != 0.1008 {
		t.Errorf("on-demand: rate=%f err=%v", rate, err)
	}

	rate, err = m.HourlyRate(ctx, "m7i.large", "us-east-1", "spot")
	if err != nil || rate != 0.04 {
		t.Errorf("spot: rate=%f err=%v", rate, err)
	}
}

func TestMockFinder_ErrorInjection(t *testing.T) {
	injected := errors.New("simulated failure")
	m := awsmock.New(awsmock.WithError(injected))

	ctx := context.Background()

	_, err := m.GetEnabledRegions(ctx)
	if !errors.Is(err, injected) {
		t.Errorf("GetEnabledRegions: expected injected error, got %v", err)
	}

	_, err = m.SearchInstanceTypes(ctx, []string{"us-east-1"}, nil, aws.FilterOptions{})
	if !errors.Is(err, injected) {
		t.Errorf("SearchInstanceTypes: expected injected error, got %v", err)
	}

	_, err = m.OnDemandPrice(ctx, "m7i.large", "us-east-1")
	if !errors.Is(err, injected) {
		t.Errorf("OnDemandPrice: expected injected error, got %v", err)
	}
}

func TestMockFinder_GetInstanceTypes(t *testing.T) {
	m := awsmock.New(
		awsmock.WithInstances([]aws.InstanceTypeResult{
			{InstanceType: "m7i.large", Region: "us-east-1"},
			{InstanceType: "m7i.xlarge", Region: "us-east-1"},
			{InstanceType: "c7g.large", Region: "us-west-2"},
		}),
	)

	types, err := m.GetInstanceTypes(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Errorf("expected 2 types in us-east-1, got %d", len(types))
	}
}

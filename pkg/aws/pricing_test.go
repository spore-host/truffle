package aws

import (
	"context"
	"math"
	"testing"
)

// stubPricer returns a fixed price per instance type for deterministic tests.
type stubPricer struct {
	prices map[string]float64
	err    error
}

func (s stubPricer) OnDemandPrice(_ context.Context, instanceType, _ string) (float64, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.prices[instanceType], nil
}

func TestParseOnDemandFromPriceItem(t *testing.T) {
	// Minimal but realistic Price List product JSON for c6i.4xlarge in us-east-1.
	item := `{
		"product": {"attributes": {"instanceType": "c6i.4xlarge", "regionCode": "us-east-1"}},
		"terms": {
			"OnDemand": {
				"ABCD1234": {
					"priceDimensions": {
						"ABCD1234.JRTCKXETXF": {
							"unit": "Hrs",
							"pricePerUnit": {"USD": "0.6800000000"}
						}
					}
				}
			}
		}
	}`

	price, ok := parseOnDemandFromPriceItem(item)
	if !ok {
		t.Fatal("parseOnDemandFromPriceItem returned ok=false for valid product")
	}
	if math.Abs(price-0.68) > 1e-9 {
		t.Errorf("price = %v, want 0.68", price)
	}
}

func TestParseOnDemandFromPriceItem_SkipsZeroAndJunk(t *testing.T) {
	// A $0.00 placeholder dimension should be skipped (no positive price).
	zero := `{"terms":{"OnDemand":{"X":{"priceDimensions":{"Y":{"unit":"Hrs","pricePerUnit":{"USD":"0.0000000000"}}}}}}}`
	if _, ok := parseOnDemandFromPriceItem(zero); ok {
		t.Error("expected ok=false for $0.00 placeholder, got ok=true")
	}

	if _, ok := parseOnDemandFromPriceItem("not json"); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

func TestStaticOnDemandPricer(t *testing.T) {
	// The static pricer never errors and returns a known table value.
	p := staticOnDemandPricer{}
	price, err := p.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("static pricer error: %v", err)
	}
	if price <= 0 {
		t.Errorf("static price = %v, want > 0", price)
	}
}

func TestFallbackPricer_UsesFallbackOnPrimaryFailure(t *testing.T) {
	f := &fallbackPricer{
		primary:  stubPricer{prices: map[string]float64{}}, // returns 0 → triggers fallback
		fallback: stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}},
	}
	price, err := f.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("fallback pricer error: %v", err)
	}
	if math.Abs(price-0.68) > 1e-9 {
		t.Errorf("price = %v, want 0.68 (from fallback)", price)
	}
}

func TestFallbackPricer_PrefersPrimary(t *testing.T) {
	f := &fallbackPricer{
		primary:  stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.70}},
		fallback: stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}},
	}
	price, _ := f.OnDemandPrice(context.Background(), "c6i.4xlarge", "us-east-1")
	if math.Abs(price-0.70) > 1e-9 {
		t.Errorf("price = %v, want 0.70 (from primary)", price)
	}
}

func TestSetOnDemandPricer_OverridesDefault(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"m6i.large": 0.096}})

	price, err := c.OnDemandPrice(context.Background(), "m6i.large", "us-east-1")
	if err != nil {
		t.Fatalf("OnDemandPrice error: %v", err)
	}
	if math.Abs(price-0.096) > 1e-9 {
		t.Errorf("price = %v, want 0.096", price)
	}
}

func TestHourlyRate_OnDemand(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}})

	for _, model := range []string{"on-demand", "ondemand", "ON-DEMAND", ""} {
		rate, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", model)
		if err != nil {
			t.Fatalf("HourlyRate(%q) error: %v", model, err)
		}
		if math.Abs(rate-0.68) > 1e-9 {
			t.Errorf("HourlyRate(%q) = %v, want 0.68", model, rate)
		}
	}
}

func TestHourlyRate_OnDemandUnavailable(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{}}) // returns 0

	if _, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", "on-demand"); err == nil {
		t.Error("expected error when on-demand price is unavailable, got nil")
	}
}

func TestHourlyRate_RejectsReservedAndUnknown(t *testing.T) {
	c := &Client{}
	c.SetOnDemandPricer(stubPricer{prices: map[string]float64{"c6i.4xlarge": 0.68}})

	if _, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", "reserved"); err == nil {
		t.Error("expected error for reserved model, got nil")
	}
	if _, err := c.HourlyRate(context.Background(), "c6i.4xlarge", "us-east-1", "bogus"); err == nil {
		t.Error("expected error for unknown model, got nil")
	}
}

package aws

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// stubSMPricer returns a fixed SageMaker price per instance type for tests.
type stubSMPricer struct {
	prices map[string]float64
	err    error
	calls  int
}

func (s *stubSMPricer) SageMakerPrice(_ context.Context, instanceType, _ string) (float64, error) {
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	return s.prices[instanceType], nil
}

// TestParseSageMakerPriceItem confirms the shared parser reads a real-shaped
// SageMaker Price List product (instanceName carries the ml. prefix; component
// is Training). The rate mirrors the live ml.g5.2xlarge Training rate.
func TestParseSageMakerPriceItem(t *testing.T) {
	item := `{
		"product": {"attributes": {
			"instanceName": "ml.g5.2xlarge",
			"regionCode": "us-east-1",
			"component": "Training"
		}},
		"terms": {
			"OnDemand": {
				"ABCD1234": {
					"priceDimensions": {
						"ABCD1234.JRTCKXETXF": {
							"unit": "Hrs",
							"pricePerUnit": {"USD": "1.5150000000"}
						}
					}
				}
			}
		}
	}`

	price, ok := parseOnDemandFromPriceItem(item)
	if !ok {
		t.Fatal("parseOnDemandFromPriceItem returned ok=false for a valid SageMaker product")
	}
	if math.Abs(price-1.515) > 1e-9 {
		t.Errorf("price = %v, want 1.515", price)
	}
}

func TestSetSageMakerPricer_OverridesDefault(t *testing.T) {
	c := &Client{}
	c.SetSageMakerPricer(&stubSMPricer{prices: map[string]float64{"ml.g5.2xlarge": 1.515}})

	price, err := c.SageMakerPrice(context.Background(), "ml.g5.2xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("SageMakerPrice error: %v", err)
	}
	if math.Abs(price-1.515) > 1e-9 {
		t.Errorf("price = %v, want 1.515", price)
	}
}

func TestSageMakerPrice_PropagatesError(t *testing.T) {
	sentinel := errors.New("no price")
	c := &Client{}
	c.SetSageMakerPricer(&stubSMPricer{err: sentinel})

	if _, err := c.SageMakerPrice(context.Background(), "ml.g5.2xlarge", "us-east-1"); !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap %v", err, sentinel)
	}
}

// smItem builds a minimal SageMaker Price List product document for one
// component at a given USD/hr rate.
func smItem(component, usd string) string {
	return `{
		"product": {"attributes": {
			"instanceName": "ml.c5d.xlarge",
			"regionCode": "us-east-1",
			"component": "` + component + `"
		}},
		"terms": {"OnDemand": {"A": {"priceDimensions": {"B": {
			"unit": "Hrs", "pricePerUnit": {"USD": "` + usd + `"}
		}}}}}
	}`
}

// TestPickSageMakerRate_PrefersCompute verifies a compute component's rate wins
// over a (slightly higher) Studio/notebook rate, regardless of ordering.
func TestPickSageMakerRate_PrefersCompute(t *testing.T) {
	list := []string{
		smItem("studio-jupyterlab", "0.2280000000"),
		smItem("Hosting", "0.2270000000"),
		smItem("Notebook", "0.2280000000"),
	}
	price, err := pickSageMakerRate(list)
	if err != nil {
		t.Fatalf("pickSageMakerRate error: %v", err)
	}
	if math.Abs(price-0.227) > 1e-9 {
		t.Errorf("price = %v, want 0.227 (the Hosting compute rate)", price)
	}
}

// TestPickSageMakerRate_FallsBackToNonCompute verifies a type offered only via
// Studio/notebook components still reports a price rather than N/A.
func TestPickSageMakerRate_FallsBackToNonCompute(t *testing.T) {
	list := []string{
		smItem("studio-codeeditor", "0.2280000000"),
		smItem("Studio-Notebook", "0.2280000000"),
	}
	price, err := pickSageMakerRate(list)
	if err != nil {
		t.Fatalf("pickSageMakerRate error: %v", err)
	}
	if math.Abs(price-0.228) > 1e-9 {
		t.Errorf("price = %v, want 0.228 (fallback rate)", price)
	}
}

func TestPickSageMakerRate_EmptyErrors(t *testing.T) {
	if _, err := pickSageMakerRate(nil); err == nil {
		t.Error("expected error for empty price list, got nil")
	}
}

// TestAWSSageMakerPricer_Caches verifies the cache short-circuits a second
// lookup for the same (type, region) within the TTL — matching the EC2 pricer.
func TestAWSSageMakerPricer_Caches(t *testing.T) {
	p := newAWSSageMakerPricer(aws.Config{})
	// Seed the cache directly (fetch would need the live Price List API).
	p.cache["ml.g5.2xlarge\x00us-east-1"] = cachedPrice{price: 1.515, fetched: time.Now()}

	price, err := p.SageMakerPrice(context.Background(), "ml.g5.2xlarge", "us-east-1")
	if err != nil {
		t.Fatalf("SageMakerPrice error: %v", err)
	}
	if math.Abs(price-1.515) > 1e-9 {
		t.Errorf("cached price = %v, want 1.515", price)
	}
}

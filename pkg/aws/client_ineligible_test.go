package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	smithy "github.com/aws/smithy-go"
)

// TestIsCapacityBlockIneligible classifies the AWS error that means "this
// instance type is not a Capacity Block type at all" — a definitive, permanent
// answer distinct from a real query failure (AccessDenied, throttling), which
// isCapacityBlockIneligible must not misclassify (#110).
func TestIsCapacityBlockIneligible(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"ineligible instance type",
			&smithy.GenericAPIError{
				Code:    "InvalidParameterValue",
				Message: "The instance type 'c7i.4xlarge' is not supported for Capacity Blocks in 'us-west-2'. Specify a compatible instance type and try again.",
			},
			true,
		},
		{
			// Same error code, different message — InvalidParameterValue is also
			// returned for other parameter problems, so the code alone isn't
			// sufficient; the message must actually say "not supported".
			"same code, unrelated message",
			&smithy.GenericAPIError{Code: "InvalidParameterValue", Message: "CapacityDurationHours must be a positive integer"},
			false,
		},
		{
			"service limit, not ineligibility",
			&smithy.GenericAPIError{
				Code:    "CapacityBlockDescribeLimitExceeded",
				Message: "Your existing service limits for this AWS account are not sufficient to use EC2 Capacity Blocks.",
			},
			false,
		},
		{"access denied", &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "denied"}, false},
		{"throttled", &smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "throttled"}, false},
		{"not an API error", errors.New("dial tcp: connection refused"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCapacityBlockIneligible(tt.err); got != tt.want {
				t.Errorf("isCapacityBlockIneligible(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestAllIneligible checks the aggregation helper that decides whether an
// all-regions-failed response gets the more specific "not eligible" message.
func TestAllIneligible(t *testing.T) {
	ineligible := func(region string) error {
		return errIneligibleWrap(region, &smithy.GenericAPIError{
			Code:    "InvalidParameterValue",
			Message: "not supported for Capacity Blocks",
		})
	}
	other := errors.New("region us-east-1: some other failure")

	tests := []struct {
		name string
		errs []error
		want bool
	}{
		{"empty", nil, false},
		{"all ineligible", []error{ineligible("us-east-1"), ineligible("us-west-2")}, true},
		{"mixed", []error{ineligible("us-east-1"), other}, false},
		{"none ineligible", []error{other}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allIneligible(tt.errs); got != tt.want {
				t.Errorf("allIneligible(%v) = %v, want %v", tt.errs, got, tt.want)
			}
		})
	}
}

// errIneligibleWrap mirrors the wrapping GetCapacityBlockOfferings does around
// a per-region ineligible error, so TestAllIneligible exercises the same
// errors.Is shape the real aggregation step checks against.
func errIneligibleWrap(region string, apiErr error) error {
	return fmt.Errorf("region %s: %w: %w", region, ErrCapacityBlockIneligible, apiErr)
}

// TestGetCapacityBlockOfferings_AllRegionsIneligible verifies the live-format
// classification end-to-end against the exact error shape AWS returns for an
// unsupported instance type (verified live against account 942542972736,
// 2026-08 — c7i.4xlarge, m5.large, g7e.4xlarge, trn2.48xlarge all produce this
// message). Substrate does not implement DescribeCapacityBlockOfferings, so
// this drives the classifier directly against a constructed error rather than
// through a live HTTP round-trip — the round-trip is exercised by
// TestIsCapacityBlockIneligible above using the same error shape.
func TestGetCapacityBlockOfferings_AllRegionsIneligible(t *testing.T) {
	regionErrs := []error{
		errIneligibleWrap("us-west-2", &smithy.GenericAPIError{
			Code:    "InvalidParameterValue",
			Message: "The instance type 'c7i.4xlarge' is not supported for Capacity Blocks in 'us-west-2'. Specify a compatible instance type and try again.",
		}),
	}
	if !allIneligible(regionErrs) {
		t.Fatal("expected allIneligible(regionErrs) to be true for a single ineligible region")
	}
	joined := errors.Join(regionErrs...)
	if !errors.Is(joined, ErrCapacityBlockIneligible) {
		t.Error("errors.Is(joined, ErrCapacityBlockIneligible) = false, want true")
	}
	if !strings.Contains(joined.Error(), "c7i.4xlarge") {
		t.Errorf("joined error lost the instance type detail: %v", joined)
	}
}

func TestErrCapacityBlockIneligibleIsDistinctFromGenericFailure(t *testing.T) {
	generic := errors.New("connection refused")
	if errors.Is(generic, ErrCapacityBlockIneligible) {
		t.Error("a generic error must not read as ErrCapacityBlockIneligible")
	}
}

// --- GetCapacityReservations / GetCapacityBlocks now honor the #63 contract too (#110) ---

// TestGetCapacityReservations_AllRegionsFail is the regression test for #110's
// finding #4: GetCapacityReservations discarded every per-region error (only
// printed under --verbose) and always returned (results, nil) — so a total
// failure (expired credentials, throttling, an SCP denial) was indistinguishable
// from "genuinely zero reservations", which for this call is the common,
// expected answer.
func TestGetCapacityReservations_AllRegionsFail(t *testing.T) {
	c := newUnreachableClient(t)

	results, err := c.GetCapacityReservations(context.Background(),
		[]string{"us-east-1", "us-west-2"}, CapacityReservationOptions{})

	if err == nil {
		t.Fatalf("expected an error when all regions fail, got nil (results=%d)", len(results))
	}
	if !strings.Contains(err.Error(), "region queries failed") {
		t.Errorf("error should explain the total failure, got: %v", err)
	}
}

// TestGetCapacityBlocks_AllRegionsFail mirrors the above for GetCapacityBlocks
// (the "blocks you already own" listing), the other function #110 named as
// sharing the same discarded-error pattern.
func TestGetCapacityBlocks_AllRegionsFail(t *testing.T) {
	c := newUnreachableClient(t)

	results, err := c.GetCapacityBlocks(context.Background(),
		[]string{"us-east-1"}, CapacityBlockOptions{InstanceTypes: []string{"p5.48xlarge"}})

	if err == nil {
		t.Fatalf("expected an error when all regions fail, got nil (results=%d)", len(results))
	}
	if !strings.Contains(err.Error(), "region queries failed") {
		t.Errorf("error should explain the total failure, got: %v", err)
	}
}

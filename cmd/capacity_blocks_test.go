package cmd

import (
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/aws"
)

func TestRoundCapacityBlockDuration(t *testing.T) {
	cases := []struct {
		in      int
		want    int
		wantErr bool
	}{
		{1, 24, false},      // round up to 1 day
		{24, 24, false},     // already valid
		{25, 48, false},     // up to 2 days
		{30, 48, false},     // up to 2 days
		{336, 336, false},   // 14 days, exact
		{337, 504, false},   // first 7-day step past 14 days → 21 days
		{504, 504, false},   // 21 days, exact
		{505, 672, false},   // up to 28 days
		{4368, 4368, false}, // 182 days, the max
		{4369, 0, true},     // over the max → error
		{0, 0, true},        // non-positive → error
	}
	for _, c := range cases {
		got, err := roundCapacityBlockDuration(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("roundCapacityBlockDuration(%d) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("roundCapacityBlockDuration(%d) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("roundCapacityBlockDuration(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestHumanizeHours(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{24, "1 day"}, {48, "2 days"}, {72, "3 days"},
		{168, "1 week"}, {336, "2 weeks"}, {504, "3 weeks"},
	}
	for _, c := range cases {
		if got := humanizeHours(c.in); got != c.want {
			t.Errorf("humanizeHours(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseFee(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"830.5900", 830.59},
		{"  264.17 ", 264.17},
		{"0", 0},
	}
	for _, c := range cases {
		if got := parseFee(c.in); got != c.want {
			t.Errorf("parseFee(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	// Unparseable / empty fees sort last (+Inf) so priced offerings win.
	for _, bad := range []string{"", "n/a", "free"} {
		if got := parseFee(bad); got <= 1e308 {
			t.Errorf("parseFee(%q) = %v, want +Inf (sorts last)", bad, got)
		}
	}
}

func TestSortOfferings(t *testing.T) {
	// Three offerings: cheap-late, mid-early, dear-early.
	mk := func(id, start, fee string) aws.CapacityBlockOfferingResult {
		return aws.CapacityBlockOfferingResult{OfferingID: id, StartDate: start, UpfrontFee: fee}
	}
	cheapLate := mk("cb-cheap", "2026-06-19T11:30:00Z", "100.00")
	midEarly := mk("cb-mid", "2026-06-18T11:30:00Z", "500.00")
	dearEarly := mk("cb-dear", "2026-06-18T11:30:00Z", "900.00")

	t.Run("price = cheapest first", func(t *testing.T) {
		got := []aws.CapacityBlockOfferingResult{dearEarly, cheapLate, midEarly}
		sortOfferings(got, "price")
		want := []string{"cb-cheap", "cb-mid", "cb-dear"}
		for i, w := range want {
			if got[i].OfferingID != w {
				t.Errorf("price sort position %d = %s, want %s", i, got[i].OfferingID, w)
			}
		}
	})

	t.Run("start = soonest first, fee breaks ties", func(t *testing.T) {
		got := []aws.CapacityBlockOfferingResult{dearEarly, cheapLate, midEarly}
		sortOfferings(got, "start")
		// Both early ones come before the late one; mid (500) before dear (900).
		want := []string{"cb-mid", "cb-dear", "cb-cheap"}
		for i, w := range want {
			if got[i].OfferingID != w {
				t.Errorf("start sort position %d = %s, want %s", i, got[i].OfferingID, w)
			}
		}
	})
}

// TestNoOfferingsMessage covers the #109 instance-count trap: an empty offering
// list for a large --count reads as "this type has no capacity blocks" when the
// truth is usually "not that many at once". Verified 2026-07-27 — at count 8 every
// accelerator type returned zero while at count 1 four of them had offerings. The
// message must therefore name the count and point at the smaller-count retry.
func TestNoOfferingsMessage(t *testing.T) {
	t.Run("names the count and suggests the retry", func(t *testing.T) {
		got := noOfferingsMessage("p5.48xlarge", 8, 24, []string{"us-east-1"})
		for _, want := range []string{
			"8 × p5.48xlarge", // the count is the likeliest cause, so it leads
			"us-east-1",       // region gates independently of count
			"24h",
			"--count 1", // the actionable next step
		} {
			if !strings.Contains(got, want) {
				t.Errorf("message missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("no retry hint at count 1", func(t *testing.T) {
		// At count 1 there is no smaller count to fall back to, so suggesting one
		// would send the reader in a circle.
		got := noOfferingsMessage("p5.48xlarge", 1, 24, []string{"us-east-1"})
		if strings.Contains(got, "--count 1") {
			t.Errorf("suggested retrying with --count 1 when already at 1:\n%s", got)
		}
		if !strings.Contains(got, "1 × p5.48xlarge") {
			t.Errorf("message does not state the query:\n%s", got)
		}
	})

	t.Run("summarizes a wide region fan-out", func(t *testing.T) {
		// A bare invocation searches every enabled region; listing 30 region names
		// would bury the count, which is the part that matters.
		many := []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2", "eu-west-1"}
		got := noOfferingsMessage("p5.48xlarge", 8, 24, many)
		if !strings.Contains(got, "5 regions") {
			t.Errorf("wide fan-out not summarized:\n%s", got)
		}

		// A handful are still worth naming — region is a real, independent cause.
		got = noOfferingsMessage("p5.48xlarge", 8, 24, []string{"us-east-1", "us-west-2"})
		if !strings.Contains(got, "us-east-1, us-west-2") {
			t.Errorf("two regions should be named:\n%s", got)
		}
	})
}

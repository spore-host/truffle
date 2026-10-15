package find

import (
	"errors"
	"slices"
	"testing"
)

func TestUnrecognizedQueryError(t *testing.T) {
	_, err := ParseQuery("gravitn zzqx")
	var uq *UnrecognizedQueryError
	if !errors.As(err, &uq) {
		t.Fatalf("err = %v, want *UnrecognizedQueryError", err)
	}
	if !slices.Equal(uq.Terms, []string{"gravitn", "zzqx"}) {
		t.Errorf("Terms = %v", uq.Terms)
	}
	if uq.Suggestions["gravitn"] != "graviton" {
		t.Errorf("Suggestions = %v, want gravitn -> graviton", uq.Suggestions)
	}
	if _, ok := uq.Suggestions["zzqx"]; ok {
		t.Errorf("zzqx should have no suggestion: %v", uq.Suggestions)
	}
}

func TestSuggestTerms(t *testing.T) {
	got := SuggestTerms([]string{"xlrage", "h10", "m9z", "zz9q", "turn", "qqqqqqqq"})
	want := map[string]string{"xlrage": "xlarge", "h10": "h100", "m9z": "m9g", "zz9q": "zz9q.*", "turn": "turin"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("SuggestTerms[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["qqqqqqqq"]; ok {
		t.Errorf("qqqqqqqq should have no suggestion: %v", got)
	}
}

func TestUnknownTermsAndFamilies(t *testing.T) {
	pq, err := ParseQuery("graviton zzqx g5 xlarge")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pq.UnknownTerms(), []string{"zzqx"}) {
		t.Errorf("UnknownTerms = %v", pq.UnknownTerms())
	}
	if !slices.Equal(pq.Families, []string{"g5"}) || !slices.Equal(pq.InstanceSizes, []string{"xlarge"}) {
		t.Errorf("Families = %v, InstanceSizes = %v", pq.Families, pq.InstanceSizes)
	}
	if !pq.HasNoMatchingFamily() {
		t.Error("graviton g5: want HasNoMatchingFamily")
	}
	var nilPQ *ParsedQuery
	if nilPQ.UnknownTerms() != nil {
		t.Error("nil query has unknown terms")
	}
}

func TestIsInstanceFamily(t *testing.T) {
	for w, want := range map[string]bool{
		"g5": true, "m7i-flex": true, "u7in-16tb": true, "p6e-gb200": true, // the last from the Nitro table only
		"g5.xlarge": false, "zzqx": false, "h100": false,
	} {
		if got := IsInstanceFamily(w); got != want {
			t.Errorf("IsInstanceFamily(%q) = %v, want %v", w, got, want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	for _, tt := range []struct {
		a, b  string
		limit int
		want  int
	}{
		{"kitten", "sitting", 3, 3},
		{"abc", "abc", 2, 0},
		{"abc", "abcdef", 2, 3}, // over the limit by length
		{"aaaa", "bbbb", 2, 3},  // over the limit part way
	} {
		if got := editDistance(tt.a, tt.b, tt.limit); got != tt.want {
			t.Errorf("editDistance(%q, %q, %d) = %d, want %d", tt.a, tt.b, tt.limit, got, tt.want)
		}
	}
}

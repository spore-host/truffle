package find

import "testing"

func TestLooksLikeRegex(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"m7i*", false},
		{"m7i.large", false},
		{"c[6-8]i", true},
		{"(p4d|p5)", true},
		{"m7.+", true},
		{"\\d+", true},
	}
	for _, tt := range tests {
		if got := looksLikeRegex(tt.pattern); got != tt.want {
			t.Errorf("looksLikeRegex(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

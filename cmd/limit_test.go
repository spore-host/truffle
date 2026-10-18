package cmd

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

type row struct{ typ, region string }

// --limit N keeps the first N instance TYPES in result order, with every
// region row of each, not the first N rows, which could split a type's
// regions (truffle#250).
func TestLimitByKeyKeepsWholeTypesInOrder(t *testing.T) {
	rows := []row{{"m8a.4xlarge", "us-east-1"}, {"m8a.4xlarge", "us-west-2"}, {"c8a.4xlarge", "us-east-1"}, {"r8a.4xlarge", "us-east-1"}, {"c8a.4xlarge", "us-west-2"}}
	kept, total := limitByKey(rows, 2, func(r row) string { return r.typ })
	if total != 3 {
		t.Errorf("total types = %d, want 3", total)
	}
	var got []string
	for _, r := range kept {
		got = append(got, r.typ+"/"+r.region)
	}
	want := "m8a.4xlarge/us-east-1 m8a.4xlarge/us-west-2 c8a.4xlarge/us-east-1 c8a.4xlarge/us-west-2"
	if strings.Join(got, " ") != want {
		t.Errorf("kept %v, want %s", got, want)
	}
	for _, n := range []int{0, -1, 3, 10} {
		if k, _ := limitByKey(rows, n, func(r row) string { return r.typ }); len(k) != len(rows) {
			t.Errorf("limit %d cut rows (%d of %d); 0, negative or >= the type count keeps all", n, len(k), len(rows))
		}
	}
}

// The note says what was left out, and only when something was.
func TestWriteLimitNote(t *testing.T) {
	var b bytes.Buffer
	writeLimitNote(&b, 5, 33, "types")
	if !strings.Contains(b.String(), "first 5 of 33 types") || !strings.Contains(b.String(), "--limit 0") {
		t.Errorf("note = %q", b.String())
	}
	b.Reset()
	writeLimitNote(&b, 33, 33, "types")
	if b.Len() != 0 {
		t.Errorf("a note was written when nothing was cut: %q", b.String())
	}
}

// Every listing path applies the flag: find's natural-language and pattern
// paths, and spot. A helper with no caller would pass the tests above.
func TestLimitFlagIsAppliedOnEveryPath(t *testing.T) {
	for file, want := range map[string]int{"find.go": 2, "spot.go": 1} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		var n int
		if file == "find.go" {
			n = len(regexp.MustCompile(`results, totalTypes := limitByKey\(results, findLimit,`).FindAllString(src, -1))
		} else {
			n = len(regexp.MustCompile(`spotResults = spotResults\[:spotLimit\]`).FindAllString(src, -1))
		}
		if n != want {
			t.Errorf("%s applies --limit %d time(s), want %d", file, n, want)
		}
	}
}

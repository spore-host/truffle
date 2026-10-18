package cmd

import (
	"fmt"
	"io"
)

// Output limits (truffle#250). `| head -N` shortened the table by cutting it:
// the bottom border, the "✓ = spawn-supported region" legend and the
// "Showing … closest fit first" line went with it. --limit renders the first N
// and keeps all of that.

// limitByKey keeps the items whose key (an instance type) is among the first n
// distinct keys, in order, so every region row of a kept type stays together.
// n <= 0 keeps everything. It returns the kept items and the number of
// distinct keys before the cut.
func limitByKey[T any](items []T, n int, key func(T) string) (kept []T, totalKeys int) {
	seen := map[string]bool{}
	var order []string
	for _, it := range items {
		k := key(it)
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
	}
	if n <= 0 || len(order) <= n {
		return items, len(order)
	}
	allow := map[string]bool{}
	for _, k := range order[:n] {
		allow[k] = true
	}
	for _, it := range items {
		if allow[key(it)] {
			kept = append(kept, it)
		}
	}
	return kept, len(order)
}

// writeLimitNote says what --limit left out, so a shortened table isn't read
// as the whole answer. Silent when nothing was cut.
func writeLimitNote(w io.Writer, shown, total int, noun string) {
	if shown >= total {
		return
	}
	_, _ = fmt.Fprintf(w, "Listed the first %d of %d %s; --limit 0 lists all\n", shown, total, noun)
}

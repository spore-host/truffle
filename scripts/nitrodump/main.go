// Command nitrodump prints the Nitro generation table as family<TAB>generation.
//
// Exists so scripts/nitro-census.sh can diff the table against AWS without
// parsing Go source — a regex over nitro.go would drift from the table the
// program actually uses, which is the kind of gap that makes a check pass while
// the thing it checks is wrong.
package main

import (
	"fmt"
	"sort"

	taws "github.com/spore-host/truffle/pkg/aws"
)

func main() {
	fams := taws.NitroFamilies()
	keys := make([]string, 0, len(fams))
	for k := range fams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s\t%d\n", k, fams[k])
	}
}

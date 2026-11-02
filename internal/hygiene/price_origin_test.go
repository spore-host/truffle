package hygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The #184 structural gates. The owner rule: a static or fallback price shown
// anywhere must say it is static, WHY the live Price List wasn't used, and WHEN
// the static data was collected. The behavioural halves are
// pkg/output TestStaticPriceIsLabelledInEveryPrinter, cmd
// TestStaticPriceIsLabelledInEveryCommand and the pkg/aws price-origin tests;
// these make sure the set they cover can't silently fall behind the code.

// priceFields are the result fields that hold an on-demand price or a figure
// derived from one. Reading one in a renderer is what makes a function a price
// render site.
var priceFields = map[string]bool{"OnDemandPrice": true, "SavingsPercent": true}

// notRenderSites are the functions that read a price field without rendering
// it, each with the reason. A function that reads one and is in neither this
// list nor its package's staticPriceRenderers registry fails the gate, so a
// new render site has to be registered (and so behaviourally tested) or
// explained here.
var notRenderSites = map[string]string{
	"cmd.runAvailable":      "stores the resolved price and origin on the report; writeObtainability renders it",
	"cmd.priceResults":      "stores each quote's price and origin on its row; the printers and reportPricing render them",
	"cmd.sortFindResults":   "orders rows by price; prints nothing",
	"cmd.staticSavingsNote": "builds the static note printSpotSummary prints; gated through printSpotSummary",
}

// TestEveryPriceRenderSiteIsGated discovers every non-test function in cmd and
// pkg/output that reads a price field, and requires each to be either in that
// package's staticPriceRenderers registry (whose entries the behavioural gate
// renders with a static price and checks for label, date and reason) or in
// notRenderSites with a reason. Registry entries and notRenderSites entries
// must name functions that exist, so neither list can go stale.
func TestEveryPriceRenderSiteIsGated(t *testing.T) {
	root := "../.."
	for _, pkg := range []struct{ name, dir string }{
		{"cmd", filepath.Join(root, "cmd")},
		{"output", filepath.Join(root, "pkg", "output")},
	} {
		funcs, readers := priceReaders(t, pkg.dir)
		if len(readers) == 0 {
			t.Fatalf("%s: discovered no function reading %v; discovery is broken", pkg.name, keys(priceFields))
		}
		registry := registeredRenderers(t, pkg.dir)
		if len(registry) == 0 {
			t.Fatalf("%s: no staticPriceRenderers registry found in its _test.go files", pkg.name)
		}
		t.Logf("%s price readers: %v; registered renderers: %v", pkg.name, readers, keys(registry))

		for _, fn := range readers {
			q := pkg.name + "." + fn
			_, isNot := notRenderSites[q]
			switch {
			case registry[fn] && isNot:
				t.Errorf("%s is both registered as a render site and listed in notRenderSites", q)
			case !registry[fn] && !isNot:
				t.Errorf("%s reads a price field but is not in %s's staticPriceRenderers (so no test proves a static price "+
					"it shows says static, why and when) nor in notRenderSites (#184)", q, pkg.name)
			}
		}
		for fn := range registry {
			if !funcs[fn] {
				t.Errorf("%s staticPriceRenderers names %s, which is not a function in the package", pkg.name, fn)
			}
		}
		for q := range notRenderSites {
			if name, fn, _ := strings.Cut(q, "."); name == pkg.name && !contains(readers, fn) {
				t.Errorf("notRenderSites lists %s, which no longer reads a price field; drop it", q)
			}
		}
	}
}

// TestEveryPricedResultCarriesItsOrigin: every pkg/aws struct with an
// OnDemandPrice field embeds PriceOrigin, so JSON, YAML and every printer get
// price_source, price_as_of and price_fallback_reason with the price. The set
// of structs is discovered, not listed.
func TestEveryPricedResultCarriesItsOrigin(t *testing.T) {
	var priced []string
	walkGo(t, filepath.Join("../..", "pkg", "aws"), func(path string, f *ast.File, fset *token.FileSet) {
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			hasPrice, hasOrigin := false, false
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					if id, ok := field.Type.(*ast.Ident); ok && id.Name == "PriceOrigin" {
						hasOrigin = true
					}
				}
				for _, name := range field.Names {
					if name.Name == "OnDemandPrice" {
						hasPrice = true
					}
				}
			}
			if hasPrice {
				priced = append(priced, ts.Name.Name)
				if !hasOrigin {
					t.Errorf("%s: %s has an OnDemandPrice but does not embed PriceOrigin, so a static rate in it can't say why or when (#184)",
						fset.Position(ts.Pos()), ts.Name.Name)
				}
			}
			return true
		})
	})
	if len(priced) == 0 {
		t.Fatal("found no pkg/aws struct with an OnDemandPrice field; discovery is broken")
	}
	t.Logf("priced result types: %v", priced)
}

// TestStaticPriceOriginOnlyFromItsConstructor: outside pkg/aws's staticOrigin,
// non-test code may only COMPARE against PriceSourceStatic, never build a
// value with it. Building one by hand is how a static price ends up without a
// date or reason; staticOrigin (behind StaticPriceOrigin) always fills both.
func TestStaticPriceOriginOnlyFromItsConstructor(t *testing.T) {
	uses := 0
	walkGo(t, "../..", func(path string, f *ast.File, fset *token.FileSet) {
		for _, decl := range f.Decls {
			fd, isFunc := decl.(*ast.FuncDecl)
			if isFunc && fd.Name.Name == "staticOrigin" {
				continue
			}
			var stack []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				id, ok := n.(*ast.Ident)
				if !ok || id.Name != "PriceSourceStatic" {
					return true
				}
				uses++
				if staticUseIsComparison(stack) {
					return true
				}
				t.Errorf("%s: PriceSourceStatic used to build a value; use aws.StaticPriceOrigin so the date and reason come with it (#184)",
					fset.Position(id.Pos()))
				return true
			})
		}
	})
	if uses == 0 {
		t.Fatal("found no use of PriceSourceStatic at all; discovery is broken")
	}
}

// staticUseIsComparison reports whether the identifier at the top of stack is
// its own declaration, an operand of == or !=, or a switch case value.
func staticUseIsComparison(stack []ast.Node) bool {
	id := stack[len(stack)-1]
	parent := stack[len(stack)-2]
	if sel, ok := parent.(*ast.SelectorExpr); ok && sel.Sel == id && len(stack) >= 3 {
		id, parent = sel, stack[len(stack)-3]
	}
	switch p := parent.(type) {
	case *ast.ValueSpec:
		for _, name := range p.Names {
			if name == id {
				return true
			}
		}
	case *ast.BinaryExpr:
		return p.Op == token.EQL || p.Op == token.NEQ
	case *ast.CaseClause:
		for _, e := range p.List {
			if e == id {
				return true
			}
		}
	}
	return false
}

// priceReaders returns the package's function names and, sorted, those whose
// body reads a priceFields selector (closures count toward their enclosing
// function).
func priceReaders(t *testing.T, dir string) (funcs map[string]bool, readers []string) {
	t.Helper()
	funcs = map[string]bool{}
	found := map[string]bool{}
	walkGo(t, dir, func(path string, f *ast.File, _ *token.FileSet) {
		if filepath.Dir(path) != filepath.Clean(dir) {
			return
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			funcs[fd.Name.Name] = true
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && priceFields[sel.Sel.Name] {
					found[fd.Name.Name] = true
				}
				return true
			})
		}
	})
	for fn := range found {
		readers = append(readers, fn)
	}
	sort.Strings(readers)
	return funcs, readers
}

// registeredRenderers parses the keys of the package-level
// `var staticPriceRenderers = map[string]...{...}` in dir's _test.go files.
func registeredRenderers(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if name.Name != "staticPriceRenderers" || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.CompositeLit)
					if !ok {
						t.Fatalf("%s: staticPriceRenderers is not a map literal", e.Name())
					}
					for _, elt := range lit.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if bl, ok := kv.Key.(*ast.BasicLit); ok && bl.Kind == token.STRING {
							if k, err := strconv.Unquote(bl.Value); err == nil {
								out[k] = true
							}
						}
					}
				}
			}
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

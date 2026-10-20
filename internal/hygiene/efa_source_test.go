package hygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const metadataImport = "github.com/spore-host/truffle/pkg/metadata"

// TestEFASupportComesFromEC2 is the structural half of the #253 gate. "h200
// efa" matched nothing because find took EFA support from
// metadata.EFACapableFamilies, a hand-kept family list that stopped at p5. A
// list like that reports a clean bill of health for every family it forgot,
// and EFA support is per type anyway. The search now reads EC2's
// NetworkInfo.EfaSupported. This checks that it stays that way:
//
//   - no non-test Go file outside pkg/metadata uses any EFA identifier
//     pkg/metadata exports. The set is discovered from pkg/metadata's
//     declarations, not listed here, and the test fails if it finds none;
//   - pkg/aws's matchesFilters, under `if ... opts.RequireEFA ...`, calls a
//     function that reads `.EfaSupported`.
//
// pkg/aws's TestEFAQueries_FixtureMetadata is the behavioural half.
func TestEFASupportComesFromEC2(t *testing.T) {
	root := "../.."
	efaNames := exportedEFANames(t, filepath.Join(root, "pkg", "metadata"))
	if len(efaNames) == 0 {
		t.Fatal("found no EFA identifiers in pkg/metadata; discovery is broken, or the list was removed (then drop this half of the gate)")
	}
	t.Logf("pkg/metadata EFA identifiers: %v", efaNames)

	var uses []string
	walkGo(t, root, func(path string, f *ast.File, fset *token.FileSet) {
		if strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(filepath.Join(root, "pkg", "metadata"))+"/") {
			return
		}
		name := importName(f, metadataImport)
		if name == "" {
			return
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && efaNames[sel.Sel.Name] {
				uses = append(uses, fset.Position(sel.Pos()).String()+": metadata."+sel.Sel.Name)
			}
			return true
		})
	})
	for _, u := range uses {
		t.Errorf("%s: EFA support taken from pkg/metadata's hand-kept list; use EC2's NetworkInfo.EfaSupported (#253)", u)
	}

	if problem := matchesFiltersReadsEfaSupported(t, filepath.Join(root, "pkg", "aws")); problem != "" {
		t.Error(problem)
	}
}

// exportedEFANames returns the exported top-level identifiers declared in the
// non-test files of dir whose name contains "EFA" (case-sensitive, so
// "CapableFamilies" is not a hit).
func exportedEFANames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	add := func(id *ast.Ident) {
		if id.IsExported() && strings.Contains(id.Name, "EFA") {
			names[id.Name] = true
		}
	}
	walkGo(t, dir, func(_ string, f *ast.File, _ *token.FileSet) {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					add(d.Name)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.ValueSpec:
						for _, n := range s.Names {
							add(n)
						}
					case *ast.TypeSpec:
						add(s.Name)
					}
				}
			}
		}
	})
	return names
}

// matchesFiltersReadsEfaSupported returns "" if pkg/aws's matchesFilters has
// an if statement whose condition reads opts.RequireEFA and calls a package
// function that selects .EfaSupported, else what is missing.
func matchesFiltersReadsEfaSupported(t *testing.T, dir string) string {
	t.Helper()
	funcs := map[string]*ast.FuncDecl{}
	walkGo(t, dir, func(path string, f *ast.File, _ *token.FileSet) {
		if filepath.Dir(path) != filepath.Clean(dir) {
			return // subpackages (awsmock) are not pkg/aws
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
				funcs[fd.Name.Name] = fd
			}
		}
	})
	readers := map[string]bool{}
	for name, fd := range funcs {
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "EfaSupported" {
				readers[name] = true
			}
			return true
		})
	}
	if len(readers) == 0 {
		return "no pkg/aws function reads .EfaSupported; EFA support must come from EC2's NetworkInfo.EfaSupported (#253)"
	}
	mf := funcs["matchesFilters"]
	if mf == nil {
		return "pkg/aws has no matchesFilters; the gate is looking in the wrong place"
	}
	found := false
	ast.Inspect(mf.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		reqEFA, callsReader := false, false
		ast.Inspect(ifs.Cond, func(m ast.Node) bool {
			switch m := m.(type) {
			case *ast.SelectorExpr:
				if m.Sel.Name == "RequireEFA" {
					reqEFA = true
				}
			case *ast.CallExpr:
				if id, ok := m.Fun.(*ast.Ident); ok && readers[id.Name] {
					callsReader = true
				}
			}
			return true
		})
		if reqEFA && callsReader {
			found = true
		}
		return true
	})
	if !found {
		return "pkg/aws matchesFilters does not check opts.RequireEFA against a function reading .EfaSupported (#253)"
	}
	return ""
}

// walkGo parses every non-test .go file under root (skipping .git, vendor and
// testdata) and calls fn with it.
func walkGo(t *testing.T, root string, fn func(path string, f *ast.File, fset *token.FileSet)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		fn(path, f, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// importName returns the name file f uses for the import path, or "" if f
// does not import it.
func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == path {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return path[strings.LastIndexByte(path, '/')+1:]
		}
	}
	return ""
}

package cmd

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestRegionalCommandsReadRegionFlag is the gate for the #202 class of bug: a
// command that talks to a regional AWS API inherits the root's --regions / -r
// (and its --region alias, which the root merges into the same variable) but
// never reads that variable, so the flag parses, shows in --help and does
// nothing. `list` shipped that way: it read a local --region of its own
// (default us-east-1), so `list --regions us-west-2` was silently ignored and
// the configured region never consulted.
//
// It gates the mechanism, not the correlate. The striking detail in #202 was a
// local flag shadowing the persistent one, but shadowing is neither necessary
// (a command with no local flag that just forgets `regions` has the same bug)
// nor sufficient (a local flag that is read is fine; spawn's shadowing gate
// had to be deleted for exactly that reason, see spawn CLAUDE.md gate rule 6).
// The defect is that the inherited flag's variable is not read on that
// command's path, so that is what this checks, per command.
//
// The package-wide "is this flag's variable read anywhere?" check (#199)
// cannot see this: `regions` is read by every other command. The question
// here is per command: is it read on the path from this command's RunE?
//
// How: type-check this package's non-test sources (imports are not resolved,
// only this package's own objects are needed), find every cobra.Command
// literal with a RunE/Run, and take the transitive closure of the package
// functions its handler references. A command whose closure constructs a
// regional AWS client (aws.NewClient, quotas.NewClient) or looks up the
// configured region (configuredRegion) is regional, and its closure must
// read the package-level `regions` variable. Identifiers are resolved by the
// type checker, so a local named `regions` does not count as a read.
func TestRegionalCommandsReadRegionFlag(t *testing.T) {
	regional, bad := commandsIgnoringRegionFlag(t, ".")
	// The sinks must still be recognised in the real code, or the gate passes
	// vacuously (e.g. after a client constructor is renamed).
	for _, want := range []string{"available", "az", "capacity", "find", "list", "quotas", "spot"} {
		if !slices.Contains(regional, want) {
			t.Errorf("gate no longer sees %q as regional (regional commands found: %v); update regionalSinks", want, regional)
		}
	}
	if len(bad) > 0 {
		t.Errorf("commands that call a regional AWS API but never read the shared --regions/--region flag "+
			"(it parses, shows in --help, and does nothing):\n  %s\n"+
			"resolve the region through the shared helpers (commandRegions, configuredOrFallbackRegions, singleRegion) (#202)",
			strings.Join(bad, "\n  "))
	}
}

// TestRegionFlagGateCatchesAnIgnoringCommand proves the gate is not vacuous: it
// reports the command that reads only a shadowing local --region, and the one
// whose only `regions` is a local variable; it passes the command that reads
// the shared flag through a helper, and the one that makes no AWS call.
func TestRegionFlagGateCatchesAnIgnoringCommand(t *testing.T) {
	dir := t.TempDir()
	src := `package fake

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/spore-host/truffle/pkg/aws"
)

var (
	regions     []string
	localRegion string
)

var shadowCmd = &cobra.Command{Use: "shadow", RunE: runShadow}

var localCmd = &cobra.Command{Use: "local [flags]", RunE: runLocal}

var goodCmd = &cobra.Command{Use: "good", RunE: func(*cobra.Command, []string) error { return runGood() }}

var offlineCmd = &cobra.Command{Use: "offline", Run: func(*cobra.Command, []string) {}}

func init() {
	shadowCmd.Flags().StringVar(&localRegion, "region", "us-east-1", "")
}

func runShadow(*cobra.Command, []string) error {
	c, _ := aws.NewClient(context.Background())
	_, err := c.GetInstanceTypes(context.Background(), localRegion)
	return err
}

func runLocal(*cobra.Command, []string) error {
	regions := []string{"us-east-1"}
	_, err := aws.NewClient(context.Background())
	_ = regions
	return err
}

func runGood() error {
	_, err := aws.NewClient(context.Background())
	_ = pick()
	return err
}

func pick() string { return regions[0] }
`
	if err := os.WriteFile(filepath.Join(dir, "fake.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	_, got := commandsIgnoringRegionFlag(t, dir)
	want := []string{"local", "shadow"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("commandsIgnoringRegionFlag = %v, want %v", got, want)
	}
}

// regionalSinks are the calls that make a command regional: constructing a
// client for a regional AWS API, or resolving the configured region.
var regionalSinks = map[string]map[string]bool{
	"github.com/spore-host/truffle/pkg/aws":    {"NewClient": true},
	"github.com/spore-host/truffle/pkg/quotas": {"NewClient": true},
	"": {"configuredRegion": true}, // this package
}

// commandsIgnoringRegionFlag returns, sorted, the name of every regional
// command in dir, and of those whose handler never reads the package-level
// `regions`.
func commandsIgnoringRegionFlag(t *testing.T, dir string) (regional, bad []string) {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no Go sources in %s", dir)
	}

	// Imports are deliberately not resolved (type-checking the AWS SDK from
	// source is slow, and only this package's objects matter), so type errors
	// about imported packages are expected and ignored.
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{
		Importer: unresolvedImporter{},
		Error:    func(error) {},
	}
	pkg, _ := conf.Check(files[0].Name.Name, fset, files, info)
	if pkg == nil {
		t.Fatal("type-check produced no package")
	}
	regionsVar, ok := pkg.Scope().Lookup("regions").(*types.Var)
	if !ok {
		t.Fatalf("package-level var `regions` (the --regions flag) not found in %s", dir)
	}

	// Package-level function bodies, by object.
	bodies := map[types.Object]*ast.BlockStmt{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
				if obj := info.Defs[fd.Name]; obj != nil {
					bodies[obj] = fd.Body
				}
			}
		}
	}

	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isCobraCommandType(lit.Type) {
				return true
			}
			name, handler := commandUseAndHandler(lit)
			if handler == nil {
				return true
			}
			isRegional, reads := closureFacts(handler, info, bodies, regionsVar)
			if isRegional {
				regional = append(regional, name)
				if !reads {
					bad = append(bad, name)
				}
			}
			return true
		})
	}
	sort.Strings(regional)
	sort.Strings(bad)
	return regional, bad
}

// closureFacts walks root and every package function it transitively
// references, reporting whether that closure hits a regional sink and whether
// it reads regionsVar.
func closureFacts(root ast.Node, info *types.Info, bodies map[types.Object]*ast.BlockStmt, regionsVar *types.Var) (regional, reads bool) {
	seen := map[types.Object]bool{}
	queue := []ast.Node{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if x, ok := n.X.(*ast.Ident); ok {
					if pn, ok := info.Uses[x].(*types.PkgName); ok && regionalSinks[pn.Imported().Path()][n.Sel.Name] {
						regional = true
					}
				}
			case *ast.Ident:
				obj := info.Uses[n]
				switch {
				case obj == nil:
				case obj == types.Object(regionsVar):
					reads = true
				case bodies[obj] != nil:
					if regionalSinks[""][obj.Name()] {
						regional = true
					}
					if !seen[obj] {
						seen[obj] = true
						queue = append(queue, bodies[obj])
					}
				}
			}
			return true
		})
	}
	return regional, reads
}

// isCobraCommandType reports whether e is `cobra.Command`.
func isCobraCommandType(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Command" {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == "cobra"
}

// commandUseAndHandler returns a cobra.Command literal's command name (the
// first word of Use) and its RunE/Run expression (nil if it has neither).
func commandUseAndHandler(lit *ast.CompositeLit) (string, ast.Expr) {
	var name string
	var handler ast.Expr
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Use":
			if bl, ok := kv.Value.(*ast.BasicLit); ok && bl.Kind == token.STRING {
				if s, err := strconv.Unquote(bl.Value); err == nil {
					if fields := strings.Fields(s); len(fields) > 0 {
						name = fields[0]
					}
				}
			}
		case "RunE", "Run":
			handler = kv.Value
		}
	}
	return name, handler
}

// unresolvedImporter returns an empty stand-in for every import, so the type
// checker still records which package each selector refers to (by path)
// without loading any dependency.
type unresolvedImporter struct{}

func (unresolvedImporter) Import(path string) (*types.Package, error) {
	if path == "unsafe" {
		return importer.Default().Import(path)
	}
	return nil, errors.New("imports are not resolved by this gate")
}

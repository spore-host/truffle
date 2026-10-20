package cmd

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindDispatchesThroughLibraryParseQuery is the structural half of the
// #263 gate. `truffle find r8g.2xlarge` worked while the library's
// find.ParseQuery refused the same query, because cmd decided "is this a
// pattern?" itself, before calling ParseQuery, and library callers
// (spore-host-mcp) never took that path. The decision now lives in
// find.ParseQuery; this checks that runFind uses it rather than deciding
// again in cmd, so the two cannot drift apart a second time:
//
//   - runFind calls find.ParseQuery;
//   - every call to runSearchWithPattern in runFind sits inside an
//     `if <q>.IsPattern()` block, where <q> is the ParseQuery result, and
//     searches <q>.Pattern;
//   - the multi-argument comparison path (allLookLikePatterns) classifies
//     each argument with find.LooksLikePattern, the function ParseQuery uses.
//
// The matches are on invocations in the AST, not on names in the source
// text, so a comment naming these functions does not satisfy the gate.
func TestFindDispatchesThroughLibraryParseQuery(t *testing.T) {
	if problems := findDispatchProblems(t, "find.go"); len(problems) > 0 {
		t.Errorf("cmd/find.go no longer dispatches through find.ParseQuery (#263):\n  %s",
			strings.Join(problems, "\n  "))
	}
}

// TestFindDispatchGateCatchesALocalDispatch proves the gate is not vacuous:
// the pre-#263 shape (cmd checks for a pattern itself, then parses) and a
// runFind that never calls find.ParseQuery are both reported.
func TestFindDispatchGateCatchesALocalDispatch(t *testing.T) {
	for name, src := range map[string]string{
		"local pattern check before ParseQuery": `package cmd
func runFind(queryStr, service string) error {
	if looksLikePattern(queryStr) {
		return runSearchWithPattern(queryStr, service)
	}
	query, err := find.ParseQuery(queryStr)
	_, _ = query, err
	return nil
}
func allLookLikePatterns(args []string) bool {
	for _, a := range args {
		if !find.LooksLikePattern(a) {
			return false
		}
	}
	return true
}`,
		"no ParseQuery, local classifier": `package cmd
func runFind(queryStr, service string) error {
	query := parseLocally(queryStr)
	if query.IsPattern() {
		return runSearchWithPattern(query.Pattern, service)
	}
	return nil
}
func allLookLikePatterns(args []string) bool {
	for _, a := range args {
		if !looksLikePattern(a) {
			return false
		}
	}
	return true
}`,
	} {
		path := filepath.Join(t.TempDir(), "find.go")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if problems := findDispatchProblems(t, path); len(problems) == 0 {
			t.Errorf("%s: gate reported nothing", name)
		}
	}
}

// findDispatchProblems returns what is wrong with runFind's dispatch in the
// Go file at path, or nil if it goes through find.ParseQuery.
func findDispatchProblems(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			funcs[fd.Name.Name] = fd
		}
	}
	var problems []string
	runFind := funcs["runFind"]
	if runFind == nil {
		return []string{"no runFind function found; the gate is looking in the wrong place"}
	}

	// The variable(s) find.ParseQuery's result is assigned to.
	parsed := map[string]bool{}
	ast.Inspect(runFind.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
			return true
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok && isPkgCall(call, "find", "ParseQuery") {
			if id, ok := as.Lhs[0].(*ast.Ident); ok {
				parsed[id.Name] = true
			}
		}
		return true
	})
	if len(parsed) == 0 {
		problems = append(problems, "runFind does not assign the result of find.ParseQuery")
	}

	// Every runSearchWithPattern call must be guarded by <q>.IsPattern() and
	// search <q>.Pattern.
	guarded := map[*ast.CallExpr]bool{}
	ast.Inspect(runFind.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		q := isPatternReceiver(ifs.Cond)
		if q == "" || !parsed[q] {
			return true
		}
		ast.Inspect(ifs.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if ok && isLocalCall(call, "runSearchWithPattern") && len(call.Args) > 0 && isSelector(call.Args[0], q, "Pattern") {
				guarded[call] = true
			}
			return true
		})
		return true
	})
	sawPatternSearch := false
	ast.Inspect(runFind.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && isLocalCall(call, "runSearchWithPattern") {
			sawPatternSearch = true
			if !guarded[call] {
				problems = append(problems, fmt.Sprintf("%s: runSearchWithPattern is called outside `if <ParseQuery result>.IsPattern()`, or not on its .Pattern",
					fset.Position(call.Pos())))
			}
		}
		return true
	})
	if !sawPatternSearch {
		problems = append(problems, "runFind never calls runSearchWithPattern; the gate is looking in the wrong place")
	}

	// The multi-argument path classifies through the library.
	if all := funcs["allLookLikePatterns"]; all == nil {
		problems = append(problems, "no allLookLikePatterns function found")
	} else {
		uses := false
		ast.Inspect(all.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isPkgCall(call, "find", "LooksLikePattern") {
				uses = true
			}
			return true
		})
		if !uses {
			problems = append(problems, "allLookLikePatterns does not call find.LooksLikePattern")
		}
	}
	return problems
}

// isPkgCall reports whether call is pkg.name(...).
func isPkgCall(call *ast.CallExpr, pkg, name string) bool {
	return isSelector(call.Fun, pkg, name)
}

// isLocalCall reports whether call is name(...), a package-local function.
func isLocalCall(call *ast.CallExpr, name string) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == name
}

// isSelector reports whether e is x.sel for the identifier x.
func isSelector(e ast.Expr, x, sel string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != sel {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == x
}

// isPatternReceiver returns q if cond is q.IsPattern(), else "".
func isPatternReceiver(cond ast.Expr) string {
	call, ok := cond.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return ""
	}
	s, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || s.Sel.Name != "IsPattern" {
		return ""
	}
	if id, ok := s.X.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE RULE: a command that prints its error and also returns it must return
// shown(err) (or go through printRefusal), because Execute prints every error
// a command returns unless it is marked. Otherwise the person reads the same
// failure twice (observed on the built binary: `afy diff` in an unlinked
// folder). Structural, not per command: every function in cmd/*.go.

// doublePrints reports, for Go source src, each place a block calls
// output.PrintError and later returns an error value (err, fmt.Errorf,
// errors.New) not passed through shown/printRefusal.
func doublePrints(t *testing.T, name, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		printed := false
		for _, st := range block.List {
			switch s := st.(type) {
			case *ast.ExprStmt:
				if isCall(s.X, "output", "PrintError") {
					printed = true
				}
			case *ast.ReturnStmt:
				if !printed {
					continue
				}
				for _, r := range s.Results {
					if unmarkedError(r) {
						found = append(found, fset.Position(s.Pos()).String())
					}
				}
			}
		}
		return true
	})
	return found
}

func isCall(e ast.Expr, pkg, fn string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == fn
}

// unmarkedError: the returned value is a bare error (the identifier err, or a
// fmt.Errorf / errors.New call). shown(...) and printRefusal(...) are calls to
// other functions, so they pass.
func unmarkedError(e ast.Expr) bool {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name == "err"
	}
	return isCall(e, "fmt", "Errorf") || isCall(e, "errors", "New")
}

func TestNoCommandPrintsAnErrorItAlsoReturns(t *testing.T) {
	// POSITIVE CONTROL: the checker sees what it is meant to catch, and lets
	// the marked forms through.
	bad := `package cmd
func a() error { output.PrintError("x"); return err }
func b() error { output.PrintError("x"); return fmt.Errorf("x") }
func c() error { output.PrintError("x"); return shown(err) }
func d() error { return err }
func e() int   { output.PrintError("x"); return 1 }`
	if got := doublePrints(t, "fixture.go", bad); len(got) != 2 {
		t.Fatalf("the checker found %v in the fixture, want exactly the two bare returns (a, b)", got)
	}

	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found (%v): the guard would pass on nothing", err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, at := range doublePrints(t, f, string(raw)) {
			t.Errorf("%s: prints its error and returns it unmarked: return shown(err) (or printRefusal)", at)
		}
	}
	if checked < 20 {
		t.Errorf("only %d command sources were read: the glob stopped matching", checked)
	}
}

package test

// The e2e nightly is the one place every live control-plane comparison in this
// repo runs against the control plane's pushed main with AETHERFY_REQUIRE_CP
// set. It runs them BY NAME (aetherfy-e2e-tests .github/workflows/
// e2e-nightly.yml, `CLI_TESTS`, over an explicit package list), so a
// comparison added here and not named there is live on dev boxes and nowhere
// else -- which is how the aetherfy.yaml field pair (internal/archive) and the
// deploy-budget pair (cmd/deploy_watch_test.go) were missing from it.
//
// So the list is checked against the code, from the code: a test that reads
// the control-plane checkout is one that reaches cperrors.Root, directly or
// through a helper in its package -- every live comparison locates the checkout
// that way, and one that did not would not honour AETHERFY_CP_ROOT either.
// Derived with go/ast, never kept by hand.
//
// The workflow is read from the SIBLING aetherfy-e2e-tests checkout, which the
// nightly has beside this one (/opt/aetherfy/aetherfy-cli and
// /opt/aetherfy/aetherfy-e2e-tests) -- and this test is itself in CLI_TESTS,
// so the nightly checks its own list against the CLI it is about to run.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/l-td/aetherfy-cli/test/cperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nightlyWorkflow = ".github/workflows/e2e-nightly.yml"

// thisGuard must be in CLI_TESTS too, or the nightly never checks its own list.
const thisGuard = "TestTheNightlyRunsEveryControlPlaneComparison"

// cpReader is one test that reads the control-plane checkout, and the package
// `go test` must be given to run it ("./cmd/").
type cpReader struct {
	Name, Pkg string
}

// controlPlaneReaders finds every Test function under root that reaches a
// call to cperrors.Root, directly or through functions of its own package.
func controlPlaneReaders(t *testing.T, root string) []cpReader {
	t.Helper()
	byDir := map[string][]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		byDir[filepath.Dir(p)] = append(byDir[filepath.Dir(p)], f)
		return nil
	})
	require.NoError(t, err)

	var out []cpReader
	for dir, files := range byDir {
		funcs := map[string]*ast.FuncDecl{}
		for _, f := range files {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Body != nil {
					funcs[fn.Name.Name] = fn
				}
			}
		}
		readers := readersAmong(funcs)
		rel, err := filepath.Rel(root, dir)
		require.NoError(t, err)
		for name := range readers {
			if strings.HasPrefix(name, "Test") {
				out = append(out, cpReader{Name: name, Pkg: "./" + filepath.ToSlash(rel) + "/"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readersAmong returns the functions that call cperrors.Root, closed over
// calls between the given functions (one package's worth).
func readersAmong(funcs map[string]*ast.FuncDecl) map[string]bool {
	calls := map[string]map[string]bool{}
	readers := map[string]bool{}
	for name, fn := range funcs {
		calls[name] = map[string]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				if x, ok := f.X.(*ast.Ident); ok && x.Name == "cperrors" && f.Sel.Name == "Root" {
					readers[name] = true
				}
			case *ast.Ident:
				calls[name][f.Name] = true
			}
			return true
		})
	}
	for changed := true; changed; {
		changed = false
		for name, callees := range calls {
			if readers[name] {
				continue
			}
			for c := range callees {
				if readers[c] {
					readers[name], changed = true, true
					break
				}
			}
		}
	}
	return readers
}

var (
	cliTestsLine = regexp.MustCompile(`(?m)^\s*CLI_TESTS="([^"]*)"\s*$`)
	// The go test invocation: its -run must be built from CLI_TESTS, and the
	// packages follow it up to the closing parenthesis of the subshell.
	goTestLine = regexp.MustCompile(`go test -count=1 -v -run "\^\(\$\(echo "\$CLI_TESTS" \| tr ' ' '\|'\)\)\\\$" ((?:\./[^\s)]+ ?)+)\)`)
)

func TestTheNightlyRunsEveryControlPlaneComparison(t *testing.T) {
	e2eRoot := filepath.Join(repoRoot, "..", "aetherfy-e2e-tests")
	raw, err := os.ReadFile(filepath.Join(e2eRoot, filepath.FromSlash(nightlyWorkflow)))
	if err != nil {
		cperrors.SkipUnlessRequired(t, "SKIPPED: no aetherfy-e2e-tests checkout with %s at %s (%v)",
			nightlyWorkflow, e2eRoot, err)
	}
	wf := strings.ReplaceAll(string(raw), "\r\n", "\n")

	m := cliTestsLine.FindAllStringSubmatch(wf, -1)
	require.Len(t, m, 1, "expected exactly one CLI_TESTS=\"...\" assignment in %s", nightlyWorkflow)
	named := strings.Fields(m[0][1])
	g := goTestLine.FindAllStringSubmatch(wf, -1)
	require.Len(t, g, 1, "expected exactly one `go test ... -run` built from CLI_TESTS in %s", nightlyWorkflow)
	pkgs := strings.Fields(g[0][1])

	readers := controlPlaneReaders(t, repoRoot)
	// Positive control: a comparison known to read the checkout through a
	// helper-free call, and one through a helper (controlPlaneSource). If
	// either vanishes the derivation has rotted, not the list.
	names := map[string]bool{}
	for _, r := range readers {
		names[r.Name] = true
	}
	require.True(t, names["TestStartDescribesExactlyTheControlPlanesReadinessValues"],
		"the derivation no longer finds a direct cperrors.Root caller: %v", readers)
	require.True(t, names["TestKnownFieldsEqualTheControlPlaneModel"],
		"the derivation no longer follows a helper to cperrors.Root: %v", readers)

	for _, r := range readers {
		assert.Contains(t, named, r.Name,
			"%s (%s) reads the control-plane checkout but is not in CLI_TESTS in aetherfy-e2e-tests %s, "+
				"so the nightly never runs it", r.Name, r.Pkg, nightlyWorkflow)
		assert.Contains(t, pkgs, r.Pkg,
			"%s lives in %s, which the nightly's go test does not list", r.Name, r.Pkg)
	}
	assert.Contains(t, named, thisGuard, "this guard must run in the nightly, or nothing checks the list there")
	assert.Contains(t, pkgs, "./test/", "this guard's package must be in the nightly's go test list")
	for _, n := range named {
		if n != thisGuard {
			assert.True(t, names[n], "CLI_TESTS names %s, which is not a test that reads the control-plane "+
				"checkout (renamed, removed, or no longer reaching cperrors.Root)", n)
		}
	}
}

// The derivation must follow a helper, and must not mark a function that
// never reaches cperrors.Root. Checked on synthetic source, because the real
// tree only shows that it finds SOMETHING.
func TestTheReaderDerivationFollowsHelpersAndNothingElse(t *testing.T) {
	src := `package x
func helper() string { return cperrors.Root("..") }
func indirect() { _ = helper() }
func TestDirect(t *testing.T) { _ = cperrors.Root("..") }
func TestViaTwoHops(t *testing.T) { indirect() }
func TestUnrelated(t *testing.T) { _ = cperrors.RootExists("x"); other() }
func other() {}
`
	f, err := parser.ParseFile(token.NewFileSet(), "x_test.go", src, 0)
	require.NoError(t, err)
	funcs := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		fn := d.(*ast.FuncDecl)
		funcs[fn.Name.Name] = fn
	}
	got := readersAmong(funcs)
	var tests []string
	for n := range got {
		if strings.HasPrefix(n, "Test") {
			tests = append(tests, n)
		}
	}
	sort.Strings(tests)
	assert.Equal(t, []string{"TestDirect", "TestViaTwoHops"}, tests)
}

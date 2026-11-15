package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLI READS NO RUNTIME NAME. Which runtimes exist, and what each one is (its language, its version,
// whether it is TypeScript, its default file), is the server's: GET /deploy-options sends the names and,
// for each, those fields (runtime_details), and `afy init` chooses by the fields. A name that is tested
// in the CLI (a prefix, a suffix, a pattern for its shape, an equality with a language's name) would
// need a CLI release the day the platform adds a runtime of a shape it does not know.
//
// So no string of the CLI's own source may carry the shape of a runtime's name or a language's word,
// except where the CLI reads the PROJECT, not the platform: internal/detect/detect.go (the language keys a
// project is detected as, and the files that say so) and the one directory a deploy leaves out. The only
// runtime the CLI names is the `dockerfile` mode. Each exemption says why, and a stale one fails.
//
// A lexical census is a tripwire for the shapes of today's names, not a proof that no other name test
// can be written; the behaviour is held by TestResolve_ARuntimeOfANewNameShapeIsResolvedByItsFields and
// TestInitChoosesARuntimeOfANewNameShapeByItsFieldsAndHintsItsOwnFile.

// nameShape is the shape of a runtime's name (python3.14, node24, node24-ts) or the word that marks one;
// languageWord is the word of a language, which only the project scanner may use.
var (
	nameShape    = regexp.MustCompile(`(?i)python3|node[0-9]|-ts\b|typescript`)
	languageWord = regexp.MustCompile(`(?i)\b(python|node|bun)\b`)
)

// allowedLanguageWords: file -> string -> why it is not a runtime name.
var allowedLanguageWords = map[string]map[string]string{
	"internal/detect/detect.go": {
		"python":          "the language key a project is detected as, the key the server's default_runtimes and runtime_details use",
		"node":            "the language key a project is detected as, and the `engines` key of its package.json",
		"bun":             "the language key a project is detected as",
		"bun.lockb":       "the file that says a project is Bun",
		"bunfig.toml":     "the file that says a project is Bun",
		".python-version": "the file a Python project states its version in",
		".node-version":   "the file a Node project states its version in",
		`requires-python\s*=\s*"[^"]*?(\d+\.\d+)`: "the pyproject.toml field that states the Python version a project asks for",
	},
	"internal/archive/common.go": {
		"node_modules": "a directory a deploy leaves out of the archive",
	},
}

type sourceFile struct{ path, text string }

// scanRuntimeNames is every string of the given Go sources that carries a runtime name's shape or a
// language's word where it may not, and, apart, every exemption that no longer matches anything.
func scanRuntimeNames(files []sourceFile) (mentions, stale []string) {
	out := &mentions
	seen := map[string]map[string]bool{}
	for _, f := range files {
		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, f.path, f.text, 0)
		if err != nil {
			*out = append(*out, f.path+": cannot be parsed: "+err.Error())
			continue
		}
		// A struct tag names a wire field (`json:"typescript"`), not a runtime.
		tags := map[token.Pos]bool{}
		ast.Inspect(tree, func(n ast.Node) bool {
			if field, ok := n.(*ast.Field); ok && field.Tag != nil {
				tags[field.Tag.Pos()] = true
			}
			return true
		})
		ast.Inspect(tree, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || tags[lit.Pos()] {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			at := f.path + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line) + " " + lit.Value
			if _, allowed := allowedLanguageWords[f.path][v]; allowed {
				if seen[f.path] == nil {
					seen[f.path] = map[string]bool{}
				}
				seen[f.path][v] = true
				return true
			}
			switch {
			case nameShape.MatchString(v):
				*out = append(*out, at+" has the shape of a runtime's name")
			case languageWord.MatchString(v):
				*out = append(*out, at+" names a language")
			}
			return true
		})
	}
	out = &stale
	for path, values := range allowedLanguageWords {
		scanned := false
		for _, f := range files {
			scanned = scanned || f.path == path
		}
		if !scanned {
			continue
		}
		for v := range values {
			if !seen[path][v] {
				*out = append(*out, path+": the exemption "+strconv.Quote(v)+" matches nothing: remove it")
			}
		}
	}
	sort.Strings(mentions)
	sort.Strings(stale)
	return mentions, stale
}

func cliSources(t *testing.T) []sourceFile {
	t.Helper()
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	var files []sourceFile
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".git", "test", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, sourceFile{rel, string(b)})
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, len(files), 20, "the scan found the CLI's sources")
	return files
}

func TestTheCLIReadsNoRuntimeName(t *testing.T) {
	files := cliSources(t)
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.path] = true
	}
	for path := range allowedLanguageWords {
		require.True(t, paths[path], "the exempted file %s was not scanned", path)
	}
	mentions, stale := scanRuntimeNames(files)
	assert.Empty(t, mentions, "the CLI chooses a runtime by the fields the server sends (runtime_details), never by reading a name")
	assert.Empty(t, stale)
}

// The controls: each shape the CLI used to read a name by is found, and what is allowed is not.
func TestTheRuntimeNameCensusFindsWhatItForbids(t *testing.T) {
	in := func(path, src string) []string {
		mentions, _ := scanRuntimeNames([]sourceFile{{path, "package p\n\n" + src}})
		return mentions
	}
	found := func(path, src, fragment string) {
		t.Helper()
		got := in(path, src)
		if assert.Len(t, got, 1, src) {
			assert.Contains(t, got[0], fragment, src)
		}
	}
	found("internal/detect/resolve.go", "var pythonName = regexp.MustCompile(`^python3\\.(\\d+)$`)\n", "has the shape of a runtime's name")
	found("internal/detect/resolve.go", "var nodeName = regexp.MustCompile(`^node(\\d+)(-ts)?$`)\n", "has the shape of a runtime's name")
	found("internal/detect/resolve.go", "var tsName = \"-ts\"\n", "has the shape of a runtime's name")
	found("cmd/init.go", "var x = strings.HasPrefix(runtime, \"node\")\n", "names a language")
	found("cmd/init.go", "var x = runtime == \"bun\"\n", "names a language")
	found("cmd/init.go", "var x = strings.HasSuffix(runtime, \"-ts\")\n", "has the shape of a runtime's name")
	found("cmd/init.go", "var x = \"python\"\n", "names a language")
	found("internal/detect/detect.go", "var x = \"python3.14\"\n", "has the shape of a runtime's name")
	found("internal/detect/detect.go", "var x = \"deno.lock\"\nvar y = \"node22\"\n", "node22")
	found("internal/detect/detect.go", "var x = \"Node.js\"\n", "names a language")

	assert.Empty(t, in("cmd/init.go", "var mode = \"dockerfile\"\nvar hint = \"Entrypoint file (e.g. main.py, index.js)\"\n"),
		"the dockerfile mode and a file's name are not runtime names")
	assert.Empty(t, in("internal/archive/common.go", "var skip = []string{\"node_modules\"}\n"),
		"a directory a deploy leaves out is not a runtime")

	var detectAllowed []string
	for v := range allowedLanguageWords["internal/detect/detect.go"] {
		detectAllowed = append(detectAllowed, strconv.Quote(v))
	}
	sort.Strings(detectAllowed)
	assert.Empty(t, in("internal/detect/detect.go", "var all = []string{"+strings.Join(detectAllowed, ", ")+"}\n"),
		"the project scanner may name the languages and the files that say so")

	_, stale := scanRuntimeNames([]sourceFile{{"internal/detect/detect.go", "package p\n\nvar all = []string{\"python\"}\n"}})
	assert.Contains(t, strings.Join(stale, "\n"), `the exemption "node" matches nothing`, "an exemption that matches nothing is stale")
}

// Package starters holds the projects `afy init --template <name>` writes.
//
// EMBEDDED, NOT DOWNLOADED. The starters ship inside the binary (go:embed), so
// `afy init --template` works offline, cannot drift from the CLI version that
// documents it, and needs no network policy decision. The templates repository
// (l-td/templates) is the place for full example agents; these are the
// smallest project of each shape the platform serves, and nothing more.
//
// ONE CATALOGUE. This list is the only place a starter name exists: the
// --template flag, --list-templates and the conformance test all read it.
// Adding a starter is a directory under files/ plus a row here.
package starters

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
)

// all: so dotfiles are embedded too: .python-version is how the Python
// starters tell detection (and uv, pyenv) which interpreter they are for.
//
//go:embed all:files
var files embed.FS

// Starter is one project `afy init --template` can write.
type Starter struct {
	Name        string
	Description string
	// Runtime and Entrypoint preset aetherfy.yaml: the starter's files are
	// written for exactly this runtime, so a prompt for either would only be
	// a chance to get it wrong.
	Runtime    string
	Entrypoint string
}

// All is the catalogue, in the order --list-templates prints it.
var All = []Starter{
	{Name: "python-fastapi", Description: "Python service: a FastAPI app exported as `app`", Runtime: "python3.12", Entrypoint: "main.py"},
	{Name: "python-litestar", Description: "Python service: a Litestar app (any ASGI app) with a WebSocket route", Runtime: "python3.12", Entrypoint: "main.py"},
	{Name: "node-express", Description: "Node service: an Express app exported as `app`", Runtime: "node22", Entrypoint: "index.js"},
	{Name: "node-ts-hono", Description: "TypeScript service: a Hono app, an ES module exported as a fetch handler", Runtime: "node22-ts", Entrypoint: "index.ts"},
}

// Find returns the named starter.
func Find(name string) (Starter, bool) {
	for _, s := range All {
		if s.Name == name {
			return s, true
		}
	}
	return Starter{}, false
}

// Names lists every starter name, for error messages.
func Names() []string {
	names := make([]string, 0, len(All))
	for _, s := range All {
		names = append(names, s.Name)
	}
	return names
}

// Files returns the starter's files, path relative to the project root ->
// content, sorted by path by the caller as needed.
func (s Starter) Files() (map[string][]byte, error) {
	root := path.Join("files", s.Name)
	out := map[string][]byte{}
	err := fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		rel := p[len(root)+1:]
		out[rel] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading starter %s: %w", s.Name, err)
	}
	return out, nil
}

// SortedPaths is the starter's file paths in a stable order.
func SortedPaths(fileMap map[string][]byte) []string {
	paths := make([]string, 0, len(fileMap))
	for p := range fileMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

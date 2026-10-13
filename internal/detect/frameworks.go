package detect

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// FRAMEWORK DETECTION, AS DATA. `afy init` reads a project's declared
// dependencies and says, per framework it recognises, what Aetherfy will do
// with it. The advice is the whole point: a customer who wrote a LangGraph
// graph or a Flask app does not know whether "export `app`" applies to them,
// and the answer differs by framework, not by runtime.
//
// ONE TABLE, FOUR CLASSES. Adding a framework is adding a row; nothing in
// cmd/init.go branches on a framework's name. The classes mirror what the
// control plane's service launcher actually does with each shape
// (aetherfy-control-plane orchestrator/image_generator.py):
//
//	Served               the export IS what the launcher serves: any ASGI app
//	                     on Python, a (req, res) listener or a fetch handler
//	                     on Node.
//	NeedsServer          a library, not a server: nothing to serve until it is
//	                     called from an app of one of the Served shapes.
//	RefusedShape         a shape the launcher refuses or cannot serve as-is,
//	                     with the one change that makes it deployable.
//	DockerfileRecommended  a program that owns its own process and state, and
//	                     belongs in a custom container.
//
// DECLARED DEPENDENCIES ONLY, never imports scanned out of source: package.json
// dependencies + devDependencies, requirements.txt, and pyproject.toml's
// [project].dependencies. A dependency is what the customer asked the image
// to install, which is the thing the advice is about.

// AdviceClass is what Aetherfy does with a detected framework.
type AdviceClass int

const (
	Served AdviceClass = iota
	NeedsServer
	RefusedShape
	DockerfileRecommended
)

// Ecosystem is where a package name is looked up.
type Ecosystem int

const (
	Python Ecosystem = iota
	Node
)

// Framework is one row of the detection table.
type Framework struct {
	// Key is the name printed in "Detected '<key>'". Stable: it is what a
	// customer sees, and for Mastra it is the wording `afy init` printed
	// before this table existed.
	Key string
	// Display is the human name used inside the advice sentence.
	Display   string
	Ecosystem Ecosystem
	// Packages are the dependency names that identify it, normalised
	// (Python: PEP 503 lowercase with '-'; Node: as published).
	Packages []string
	Class    AdviceClass
	// WayOut is the one change that makes a RefusedShape deployable, and
	// what a DockerfileRecommended program should use instead.
	WayOut string
}

// Frameworks is the detection table, in the order advice is printed.
var Frameworks = []Framework{
	// ── Served: exported as `app`, no wrapper.
	{Key: "fastapi", Display: "FastAPI", Ecosystem: Python, Packages: []string{"fastapi"}, Class: Served},
	{Key: "starlette", Display: "Starlette", Ecosystem: Python, Packages: []string{"starlette"}, Class: Served},
	{Key: "litestar", Display: "Litestar", Ecosystem: Python, Packages: []string{"litestar"}, Class: Served},
	{Key: "quart", Display: "Quart", Ecosystem: Python, Packages: []string{"quart"}, Class: Served},
	{Key: "express", Display: "Express", Ecosystem: Node, Packages: []string{"express"}, Class: Served},
	{Key: "hono", Display: "Hono", Ecosystem: Node, Packages: []string{"hono"}, Class: Served},

	// ── NeedsServer: a library inside an app of a Served shape.
	{Key: "langgraph", Display: "LangGraph", Ecosystem: Python, Packages: []string{"langgraph"}, Class: NeedsServer},
	{Key: "crewai", Display: "CrewAI", Ecosystem: Python, Packages: []string{"crewai"}, Class: NeedsServer},
	{Key: "openai-agents", Display: "the OpenAI Agents SDK", Ecosystem: Python, Packages: []string{"openai-agents"}, Class: NeedsServer},
	{Key: "@openai/agents", Display: "the OpenAI Agents SDK", Ecosystem: Node, Packages: []string{"@openai/agents"}, Class: NeedsServer},
	{Key: "claude-agent-sdk", Display: "the Claude Agent SDK", Ecosystem: Python, Packages: []string{"claude-agent-sdk"}, Class: NeedsServer},
	{Key: "@anthropic-ai/claude-agent-sdk", Display: "the Claude Agent SDK", Ecosystem: Node, Packages: []string{"@anthropic-ai/claude-agent-sdk"}, Class: NeedsServer},
	{Key: "google-adk", Display: "Google ADK", Ecosystem: Python, Packages: []string{"google-adk"}, Class: NeedsServer},
	{Key: "pydantic-ai", Display: "Pydantic AI", Ecosystem: Python, Packages: []string{"pydantic-ai"}, Class: NeedsServer},
	// BOTH NAMES: '@mastra/core' is the package a Mastra project depends on;
	// 'mastra' is its CLI, and the only name `afy init` matched before this
	// table. The key stays 'mastra' so "Detected 'mastra'" still prints.
	{Key: "mastra", Display: "Mastra", Ecosystem: Node, Packages: []string{"@mastra/core", "mastra"}, Class: NeedsServer},
	{Key: "ai", Display: "the AI SDK", Ecosystem: Node, Packages: []string{"ai"}, Class: NeedsServer},

	// ── RefusedShape: deployable after one change.
	{Key: "flask", Display: "Flask", Ecosystem: Python, Packages: []string{"flask"}, Class: RefusedShape,
		WayOut: "wrap it with asgiref.wsgi.WsgiToAsgi (`app = WsgiToAsgi(flask_app)`), or set `runtime: dockerfile`"},
	{Key: "django", Display: "Django", Ecosystem: Python, Packages: []string{"django"}, Class: RefusedShape,
		WayOut: "wrap its WSGI handler with asgiref.wsgi.WsgiToAsgi, or set `runtime: dockerfile`"},
	{Key: "fastify", Display: "Fastify", Ecosystem: Node, Packages: []string{"fastify"}, Class: RefusedShape,
		WayOut: "export `{ app, server }` with `server = app.server` after `await app.ready()`"},
	{Key: "koa", Display: "Koa", Ecosystem: Node, Packages: []string{"koa"}, Class: RefusedShape,
		WayOut: "export `app.callback()` as `app`, or `{ app, server }`"},

	// ── DockerfileRecommended.
	{Key: "openclaw", Display: "OpenClaw", Ecosystem: Node, Packages: []string{"openclaw"}, Class: DockerfileRecommended,
		WayOut: "`runtime: dockerfile`"},
}

// Advice is the sentence `afy init` prints after "Detected '<key>' in
// dependencies — ". One sentence per class, filled from the row.
func (f Framework) Advice() string {
	switch f.Class {
	case Served:
		return f.Display + " is served as your exported `app`, with no wrapper."
	case NeedsServer:
		server := "a FastAPI or Litestar `app`"
		if f.Ecosystem == Node {
			server = "a Hono or Express `app`"
		}
		return f.Display + " is a library, not a server: call it from " + server + " for a service, or run it from a task's entrypoint."
	case RefusedShape:
		return f.Display + " cannot be served as it is exported: " + f.WayOut + "."
	case DockerfileRecommended:
		return f.Display + " runs its own long-lived process; deploy it with " + f.WayOut + "."
	}
	return ""
}

// DetectFrameworks returns every table row whose package the project in dir
// declares, in table order. A row is reported once however many of its
// packages match.
func DetectFrameworks(dir string) []Framework {
	declared := map[Ecosystem]map[string]bool{
		Python: pythonDependencies(dir),
		Node:   nodeDependencies(filepath.Join(dir, "package.json")),
	}
	var found []Framework
	for _, f := range Frameworks {
		for _, pkg := range f.Packages {
			if declared[f.Ecosystem][pkg] {
				found = append(found, f)
				break
			}
		}
	}
	return found
}

// nodeDependencies is every key of dependencies and devDependencies.
func nodeDependencies(packageJSONPath string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return out
	}
	var pkg struct {
		Dependencies    map[string]any `json:"dependencies"`
		DevDependencies map[string]any `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return out
	}
	for _, deps := range []map[string]any{pkg.Dependencies, pkg.DevDependencies} {
		for name := range deps {
			out[name] = true
		}
	}
	return out
}

// requirementName is the distribution name at the start of a PEP 508
// requirement: everything before extras, a version specifier, a marker or a
// URL. "fastapi[standard]>=0.115; python_version>'3.10'" → "fastapi".
var requirementName = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)

// normalisePythonName is PEP 503: case-insensitive, and runs of '-', '_' and
// '.' are the same character.
var pep503Separators = regexp.MustCompile(`[-_.]+`)

func normalisePythonName(name string) string {
	return pep503Separators.ReplaceAllString(strings.ToLower(name), "-")
}

// pythonDependencies reads requirements.txt and pyproject.toml's
// [project].dependencies.
func pythonDependencies(dir string) map[string]bool {
	out := map[string]bool{}
	add := func(requirement string) {
		if m := requirementName.FindStringSubmatch(requirement); m != nil {
			out[normalisePythonName(m[1])] = true
		}
	}

	if f, err := os.Open(filepath.Join(dir, "requirements.txt")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			// Comments, and pip options (-r, -e, --index-url) name no
			// distribution this table could match.
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
				continue
			}
			add(line)
		}
		f.Close()
	}

	if data, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		var doc struct {
			Project struct {
				Dependencies []string `toml:"dependencies"`
			} `toml:"project"`
		}
		// An unparseable pyproject.toml detects nothing from it; the build
		// reports the file itself, with a better message than init could.
		if toml.Unmarshal(data, &doc) == nil {
			for _, requirement := range doc.Project.Dependencies {
				add(requirement)
			}
		}
	}
	return out
}

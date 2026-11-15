package detect

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// Resolution is the runtime a project runs on, and what to tell the person when it is not
// the version they asked for.
type Resolution struct {
	Runtime string
	// Note is empty when the project got what it asked for.
	Note string
}

// Resolve turns what a project asks for into one of the runtimes the platform offers.
//
// A runtime is chosen by what it IS, from the fields the server sends for it (GET /deploy-options:
// runtime_details: its language, its version, whether it is TypeScript), and never by reading its name:
// a runtime of any name the platform adds resolves with no CLI release.
//
//   - A Dockerfile is the one mode that is a runtime of its own.
//   - A project that names no version gets the platform's default for its language
//     (defaults, from the server), in the project's flavour (TypeScript or not); a language the
//     server names no default for resolves when it offers exactly one runtime of it.
//   - A version the platform offers is that runtime.
//   - A version it does not offer (one that reached its end of life, or one newer than it
//     runs) gets the lowest offered version at or above it, or the newest offered when there
//     is none, and the Note says so.
//
// A project of no recognised language resolves to nothing, with no error: the caller asks.
func Resolve(hints ProjectHints, offered []string, defaults map[string]string, details map[string]api.RuntimeDetail) (Resolution, error) {
	switch hints.Language {
	case "":
		return Resolution{}, nil
	case Dockerfile:
		if !contains(offered, Dockerfile) {
			return Resolution{}, fmt.Errorf("the server does not offer the %s runtime", Dockerfile)
		}
		return Resolution{Runtime: Dockerfile}, nil
	case Python, Node, Bun:
	default:
		return Resolution{}, fmt.Errorf("unknown language %q", hints.Language)
	}

	candidates := candidatesOf(hints, offered, details)
	if len(candidates) == 0 {
		return Resolution{}, fmt.Errorf("the server offers no %s runtime", hints.Language)
	}

	if hints.Version == "" {
		return defaultOf(hints, offered, candidates, defaults, details)
	}

	want, ok := parseVersion(hints.Version)
	if !ok {
		return Resolution{}, fmt.Errorf("cannot read the %s version %q", hints.Language, hints.Version)
	}
	chosen, note := nearest(candidates, want)
	if note != "" {
		note = fmt.Sprintf("%s %s is %s; using %s", languageLabel(hints.Language), hints.Version, note, chosen.name)
	}
	return Resolution{Runtime: chosen.name, Note: note}, nil
}

type candidate struct {
	name    string
	version []int
	raw     string
}

// candidatesOf are the offered runtimes of the project's language, and of its flavour (the TypeScript
// ones when the project is TypeScript), ascending by version.
func candidatesOf(hints ProjectHints, offered []string, details map[string]api.RuntimeDetail) []candidate {
	var out []candidate
	for _, name := range offered {
		d, ok := details[name]
		if !ok || d.Language != hints.Language || d.TypeScript != hints.TypeScript {
			continue
		}
		v, ok := parseVersion(d.Version)
		if !ok {
			continue
		}
		out = append(out, candidate{name: name, version: v, raw: d.Version})
	}
	sort.SliceStable(out, func(i, j int) bool { return compareVersions(out[i].version, out[j].version) < 0 })
	return out
}

// defaultOf is the server's default runtime for the project's language, in the project's flavour: the
// candidate carrying the default's own version.
func defaultOf(hints ProjectHints, offered []string, candidates []candidate, defaults map[string]string, details map[string]api.RuntimeDetail) (Resolution, error) {
	name := defaults[hints.Language]
	if name == "" {
		if len(candidates) == 1 {
			return Resolution{Runtime: candidates[0].name}, nil
		}
		return Resolution{}, fmt.Errorf("the server named no default %s runtime", hints.Language)
	}
	d, ok := details[name]
	if !ok || !contains(offered, name) {
		return Resolution{}, fmt.Errorf("the server's default %s runtime, %s, is not one it offers", hints.Language, name)
	}
	for _, c := range candidates {
		if c.raw == d.Version {
			return Resolution{Runtime: c.name}, nil
		}
	}
	return Resolution{}, fmt.Errorf("the server's default %s runtime, %s, has no variant of this project's kind", hints.Language, name)
}

// parseVersion reads "3.14" or "24" as its numeric parts.
func parseVersion(raw string) ([]int, bool) {
	if raw == "" {
		return nil, false
	}
	var out []int
	for _, part := range strings.Split(raw, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// compareVersions orders two versions part by part; a shorter one that is a prefix of the other is the lower.
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// nearest is the offered version equal to want, else the lowest above it, else the highest
// there is; the second result says why it was not equal.
func nearest(candidates []candidate, want []int) (candidate, string) {
	for _, c := range candidates {
		if compareVersions(c.version, want) == 0 {
			return c, ""
		}
	}
	for _, c := range candidates {
		if compareVersions(c.version, want) > 0 {
			return c, "not offered"
		}
	}
	return candidates[len(candidates)-1], "newer than any offered"
}

// languageLabel is what a person is told the language is: its key, capitalised.
func languageLabel(language string) string {
	return strings.ToUpper(language[:1]) + language[1:]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

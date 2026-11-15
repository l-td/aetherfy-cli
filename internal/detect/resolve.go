package detect

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Resolution is the runtime a project runs on, and what to tell the person when it is not
// the version they asked for.
type Resolution struct {
	Runtime string
	// Note is empty when the project got what it asked for.
	Note string
}

// The runtimes of one language are named <prefix><version>, and the TypeScript ones add
// "-ts". These are the shapes of the platform's names, never a list of them: which runtimes
// exist is the server's (GET /deploy-options).
var (
	pythonName = regexp.MustCompile(`^python3\.(\d+)$`)
	nodeName   = regexp.MustCompile(`^node(\d+)(-ts)?$`)
)

// Resolve turns what a project asks for into one of the runtimes the platform offers.
//
//   - A mode that is its own runtime (a Dockerfile, Bun) is that runtime.
//   - A project that names no version gets the platform's default for its language
//     (defaults, from the server).
//   - A version the platform offers is that runtime.
//   - A version it does not offer (one that reached its end of life, or one newer than it
//     runs) gets the lowest offered version at or above it, or the newest offered when there
//     is none, and the Note says so.
//
// A project of no recognised language resolves to nothing, with no error: the caller asks.
func Resolve(hints ProjectHints, offered []string, defaults map[string]string) (Resolution, error) {
	switch hints.Language {
	case "":
		return Resolution{}, nil
	case Dockerfile, Bun:
		if !contains(offered, hints.Language) {
			return Resolution{}, fmt.Errorf("the server does not offer the %s runtime", hints.Language)
		}
		return Resolution{Runtime: hints.Language}, nil
	case Python, Node:
	default:
		return Resolution{}, fmt.Errorf("unknown language %q", hints.Language)
	}

	versions := offeredVersions(hints, offered)
	if len(versions) == 0 {
		return Resolution{}, fmt.Errorf("the server offers no %s runtime", hints.Language)
	}

	if hints.Version == "" {
		name := defaults[hints.Language]
		if name == "" {
			return Resolution{}, fmt.Errorf("the server named no default %s runtime", hints.Language)
		}
		if hints.TypeScript {
			name += "-ts"
		}
		if !contains(offered, name) {
			return Resolution{}, fmt.Errorf("the server's default %s runtime, %s, is not one it offers", hints.Language, name)
		}
		return Resolution{Runtime: name}, nil
	}

	want, err := versionNumber(hints)
	if err != nil {
		return Resolution{}, err
	}
	chosen, note := nearest(versions, want)
	runtime := chosen.name
	if note != "" {
		note = fmt.Sprintf("%s %s is %s; using %s", languageLabel(hints.Language), hints.Version, note, runtime)
	}
	return Resolution{Runtime: runtime, Note: note}, nil
}

type offeredVersion struct {
	number int // the minor (Python) or the major (Node)
	name   string
}

// offeredVersions are the offered runtimes of the project's language (the TypeScript ones
// when the project is TypeScript), ascending.
func offeredVersions(hints ProjectHints, offered []string) []offeredVersion {
	var out []offeredVersion
	for _, name := range offered {
		var match []string
		switch hints.Language {
		case Python:
			match = pythonName.FindStringSubmatch(name)
		case Node:
			match = nodeName.FindStringSubmatch(name)
			if match != nil && (match[2] == "-ts") != hints.TypeScript {
				match = nil
			}
		}
		if match == nil {
			continue
		}
		n, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		out = append(out, offeredVersion{number: n, name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	return out
}

// versionNumber is the number Version carries that offered versions are compared by.
func versionNumber(hints ProjectHints) (int, error) {
	raw := hints.Version
	if hints.Language == Python {
		raw = strings.TrimPrefix(raw, "3.")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("cannot read the %s version %q", hints.Language, hints.Version)
	}
	return n, nil
}

// nearest is the offered version equal to want, else the lowest above it, else the highest
// there is; the second result says why it was not equal.
func nearest(versions []offeredVersion, want int) (offeredVersion, string) {
	for _, v := range versions {
		if v.number == want {
			return v, ""
		}
	}
	for _, v := range versions {
		if v.number > want {
			return v, "not offered"
		}
	}
	return versions[len(versions)-1], "newer than any offered"
}

func languageLabel(language string) string {
	if language == Python {
		return "Python"
	}
	return "Node"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

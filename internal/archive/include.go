package archive

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// `include:` in aetherfy.yaml: repository paths deployed with an agent besides its own folder, each
// relative to that folder and leaving it ("../../shared"). The control plane holds the same rules
// (aetherfy-control-plane shared/config_parser.include_problem for the grammar, shared/code_tree for the
// rest) and refuses with the same sentences; the CLI refuses before it uploads.

// IncludeMax is the most paths one aetherfy.yaml may include.
const IncludeMax = 20

// IncludeMaxLen is an include's longest spelling, in characters: the root folder's.
const IncludeMaxLen = 512

var (
	includeNameRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	includeDriveRE = regexp.MustCompile(`^[A-Za-z]:`)
)

// Codes of the control plane's include refusals (its shared/error_codes.py), named here so a refusal
// before the upload says which one the server would have answered.
const (
	CodeIncludeOutsideRepository = "DEPLOYMENT_INCLUDE_OUTSIDE_REPOSITORY"
	CodeIncludeNotFound          = "DEPLOYMENT_INCLUDE_NOT_FOUND"
	CodeIncludeOverlapsFolder    = "DEPLOYMENT_INCLUDE_OVERLAPS_AGENT_FOLDER"
	CodeConfigParseError         = "DEPLOYMENT_CONFIG_PARSE_ERROR"
)

// IncludeError is an include refused before the upload: the control plane's code and sentence.
type IncludeError struct {
	Code    string
	Message string
}

func (e *IncludeError) Error() string { return e.Message }

func configRefusal(msg string) *IncludeError {
	return &IncludeError{Code: CodeConfigParseError, Message: "Config validation failed: include: " + msg}
}

// IncludeKey is an include as written, without its trailing '/': two spellings of one path are one include.
func IncludeKey(entry string) string { return strings.TrimSuffix(entry, "/") }

// IncludeProblem says why an include entry is refused, as a sentence, or "" when it is accepted. The
// grammar: '../' steps, then the names of the shared path, '/'-separated, a trailing '/' allowed.
func IncludeProblem(entry string) string {
	if strings.Contains(entry, `\`) {
		return "Use '/' as the path separator."
	}
	if strings.HasPrefix(entry, "/") || includeDriveRE.MatchString(entry) {
		return "An include is relative to the agent's folder, not an absolute path."
	}
	if utf8.RuneCountInString(entry) > IncludeMaxLen {
		return fmt.Sprintf("An include is at most %d characters.", IncludeMaxLen)
	}
	parts := strings.Split(IncludeKey(entry), "/")
	allSteps := true
	for _, p := range parts {
		if p != ".." {
			allSteps = false
		}
	}
	if allSteps {
		return "It names a folder that contains the agent's own folder: include only the folders the agent " +
			"shares, not one above it."
	}
	if !strings.HasPrefix(entry, "../") {
		return "It names a path inside the agent's own folder, which is always deployed: an include starts " +
			"with '../' and names a path outside it."
	}
	steps := 0
	for steps < len(parts) && parts[steps] == ".." {
		steps++
	}
	for _, name := range parts[steps:] {
		if name == "." || name == ".." || !includeNameRE.MatchString(name) {
			return "After the leading '../' steps, use folder and file names made of letters, digits, '.', " +
				"'_' and '-', separated by '/'."
		}
	}
	return ""
}

// Includes reads the yaml's `include:` value (decoded as `any`) as entries, refusing what the control
// plane's model refuses before any path is looked at: not a list, too many, a bad entry, one path twice,
// a path inside another.
func Includes(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, configRefusal("include is a list of paths, one per line, each starting with '../'.")
	}
	if len(list) > IncludeMax {
		return nil, configRefusal(fmt.Sprintf("include lists %d paths; the most it takes is %d.", len(list), IncludeMax))
	}
	entries := make([]string, 0, len(list))
	for _, v := range list {
		s, isString := v.(string)
		if !isString {
			return nil, configRefusal(fmt.Sprintf("Invalid include entry '%v'. Each include is a path, written as a string.", v))
		}
		if problem := IncludeProblem(s); problem != "" {
			return nil, configRefusal(fmt.Sprintf("Invalid include entry '%s'. %s", s, problem))
		}
		entries = append(entries, s)
	}
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = IncludeKey(e)
		for _, earlier := range keys[:i] {
			if earlier == keys[i] {
				return nil, configRefusal(fmt.Sprintf("include lists '%s' twice.", keys[i]))
			}
		}
	}
	for _, a := range keys {
		for _, b := range keys {
			if a != b && strings.HasPrefix(a, b+"/") {
				return nil, configRefusal(fmt.Sprintf("include lists '%s', which is inside '%s', also listed: list only '%s'.", a, b, b))
			}
		}
	}
	return entries, nil
}

func folderWords(codeRoot string) string {
	if codeRoot == "" {
		return "the repository root"
	}
	return "'" + codeRoot + "'"
}

func inside(p, folder string) bool { return p == folder || strings.HasPrefix(p, folder+"/") }

// ResolvedInclude is an include and the repository path it names.
type ResolvedInclude struct {
	Entry string
	Path  string
}

// ResolveIncludes gives each include's repository path, from the agent's folder `codeRoot` (repository
// relative, "/"-separated; "" is the repository root), refusing a path that leaves the repository or
// overlaps the agent's folder, and two entries naming one path or one inside another.
func ResolveIncludes(codeRoot string, entries []string) ([]ResolvedInclude, error) {
	var out []ResolvedInclude
	for _, entry := range entries {
		base := codeRoot
		if base == "" {
			base = "."
		}
		p := path.Clean(path.Join(base, IncludeKey(entry)))
		if p == ".." || strings.HasPrefix(p, "../") {
			return nil, &IncludeError{CodeIncludeOutsideRepository, fmt.Sprintf(
				"include '%s' leaves the repository: from %s it reaches above the repository root. Include only "+
					"paths inside the repository.", entry, folderWords(codeRoot))}
		}
		if codeRoot != "" && inside(p, codeRoot) {
			what := "a path inside this agent's own folder"
			if p == codeRoot {
				what = "this agent's own folder"
			}
			return nil, &IncludeError{CodeIncludeOverlapsFolder, fmt.Sprintf(
				"include '%s' names '%s', %s (%s), which is always deployed: include only paths outside it.",
				entry, p, what, folderWords(codeRoot))}
		}
		if p == "." || (codeRoot != "" && inside(codeRoot, p)) {
			return nil, &IncludeError{CodeIncludeOverlapsFolder, fmt.Sprintf(
				"include '%s' names '%s', which contains this agent's own folder (%s). The agent's folder is "+
					"always deployed: include only the folders it shares.", entry, p, folderWords(codeRoot))}
		}
		out = append(out, ResolvedInclude{entry, p})
	}
	for i, r := range out {
		for _, earlier := range out[:i] {
			if earlier.Path == r.Path {
				return nil, &IncludeError{CodeConfigParseError, fmt.Sprintf("include lists '%s' twice.", r.Path)}
			}
		}
		for _, other := range out {
			if other.Path != r.Path && inside(r.Path, other.Path) {
				return nil, &IncludeError{CodeConfigParseError, fmt.Sprintf(
					"include lists '%s', which is inside '%s', also listed: list only '%s'.", r.Path, other.Path, other.Path)}
			}
		}
	}
	return out, nil
}

// realPath follows every link of p; a path that does not exist is cleaned as written.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// CheckIncludesOnDisk holds each resolved include against the repository's files: it must exist and,
// following any link on its way, stay inside the repository and off the agent's folder.
func CheckIncludesOnDisk(repoRoot, codeRoot string, resolved []ResolvedInclude) error {
	repoReal := realPath(repoRoot)
	folderReal := realPath(filepath.Join(repoRoot, filepath.FromSlash(codeRoot)))
	for _, r := range resolved {
		full := filepath.Join(repoRoot, filepath.FromSlash(r.Path))
		if _, err := os.Lstat(full); err != nil {
			return &IncludeError{CodeIncludeNotFound, fmt.Sprintf(
				"include '%s' names '%s', which is not in this repository.", r.Entry, r.Path)}
		}
		target := realPath(full)
		if !within(target, repoReal) {
			return &IncludeError{CodeIncludeOutsideRepository, fmt.Sprintf(
				"include '%s' leaves the repository through a link: '%s' leads to a path outside it. Include "+
					"only paths inside the repository.", r.Entry, r.Path)}
		}
		if _, err := os.Stat(full); err != nil {
			return &IncludeError{CodeIncludeNotFound, fmt.Sprintf(
				"include '%s' names '%s', which is not in this repository.", r.Entry, r.Path)}
		}
		if within(target, folderReal) || within(folderReal, target) {
			return &IncludeError{CodeIncludeOverlapsFolder, fmt.Sprintf(
				"include '%s' leads through a link to this agent's own folder (%s), which is always deployed: "+
					"include only the folders it shares.", r.Entry, folderWords(codeRoot))}
		}
	}
	return nil
}

// ErrNotInRepository is a folder whose aetherfy.yaml includes paths but that is not inside a git repository.
var ErrNotInRepository = errors.New("aetherfy.yaml includes paths, but this folder is not inside a git repository: " +
	"Aetherfy needs the repository's root to know what may be included. Deploy it from a clone of its repository.")

// RepositoryRoot is the nearest folder at or above dir that holds `.git` (a folder, or a file as a
// worktree has), the rule .aetherfy/agent.json's git check uses; no git binary is needed.
func RepositoryRoot(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", ErrNotInRepository
		}
		d = parent
	}
}

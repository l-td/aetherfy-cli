// Package link is how a folder names the agent it deploys to:
// .aetherfy/agent.json, a file the CLI owns and the user commits.
//
//	{ "targets": { "default": "<agent id>", "staging": "<agent id>" } }
//
// AN ID, NEVER A NAME. A rename or a move does not unlink a folder, and a
// deploy cannot land on a different agent that later took the old name.
// Targets are the one way to deploy one codebase to several agents.
package link

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Dir is the folder the link lives in, beside aetherfy.yaml.
	Dir = ".aetherfy"
	// FileName is the link's file inside Dir.
	FileName = "agent.json"
	// DefaultTarget is the target used when none is named.
	DefaultTarget = "default"
)

// ErrNotLinked is a folder with no link file.
var ErrNotLinked = errors.New("not linked")

// File is the link file's content.
type File struct {
	Targets map[string]string `json:"targets"`
}

// Path is the link file of folder dir.
func Path(dir string) string { return filepath.Join(dir, Dir, FileName) }

// RelPath is the link file's path relative to its folder, slash-separated,
// as git names it.
func RelPath() string { return Dir + "/" + FileName }

// Read reads dir's link file. A folder without one is ErrNotLinked; a file
// that is not a link (bad JSON, no targets, an empty id) is an error naming it.
func Read(dir string) (*File, error) {
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotLinked
		}
		return nil, err
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s is not a link file: %v", Path(dir), err)
	}
	if len(f.Targets) == 0 {
		return nil, fmt.Errorf("%s has no targets", Path(dir))
	}
	for t, id := range f.Targets {
		if strings.TrimSpace(t) == "" || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("%s has an empty target or agent id", Path(dir))
		}
	}
	return &f, nil
}

// Names are the file's targets, sorted.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Targets))
	for t := range f.Targets {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Write links dir's target to agentID, keeping every other target, and
// creates the file (and Dir) when there is none. An unreadable existing file
// is not overwritten: it may hold targets someone needs.
func Write(dir, target, agentID string) error {
	f, err := Read(dir)
	switch {
	case errors.Is(err, ErrNotLinked):
		f = &File{Targets: map[string]string{}}
	case err != nil:
		return err
	}
	f.Targets[target] = agentID
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path(dir), append(raw, '\n'), 0o644)
}

// GitState is whether git will carry the link file to everyone else who
// deploys this folder.
type GitState int

const (
	// NotInGit: the folder is not in a git work tree (or git is absent).
	NotInGit GitState = iota
	// Tracked: committed or staged; nothing to say.
	Tracked
	// Untracked: in a work tree, not added.
	Untracked
	// Ignored: a .gitignore rule hides it.
	Ignored
)

// Git reports dir's link file's state in git.
func Git(dir string) GitState {
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdout, cmd.Stderr = nil, nil
		return cmd.Run()
	}
	if git("rev-parse", "--is-inside-work-tree") != nil {
		return NotInGit
	}
	if git("ls-files", "--error-unmatch", "--", RelPath()) == nil {
		return Tracked
	}
	if git("check-ignore", "-q", "--", RelPath()) == nil {
		return Ignored
	}
	return Untracked
}

package cmd

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
)

// pickWorkspace resolves the workspace a command acts in. Every agent and
// collection lives in exactly one, so there is always an answer to find:
//
//   - named (a --workspace flag): that workspace, looked up by name, which a
//     person types; the control plane answers WORKSPACE_NOT_FOUND otherwise;
//   - the account has one workspace: that one, without asking;
//   - several, on a terminal: asked, by number;
//   - several, without a terminal: refused (exit 2), naming them, since a
//     script must never hang on a question nobody can answer.
//
// No environment variable is read: a person names the workspace.
func pickWorkspace(cp *api.Client, name string, named, interactive bool, in io.Reader, out io.Writer) (*api.Workspace, error) {
	if named {
		if name == "" {
			return nil, refuse("--workspace got an empty workspace name: everything lives in a named workspace. Nothing was sent.")
		}
		return cp.GetWorkspace(name)
	}
	all, err := cp.ListWorkspaces()
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	switch {
	case len(all) == 0:
		// Every account gets one at signup and the last one cannot be
		// deleted, so this is the server breaking its own rule; say so.
		return nil, fmt.Errorf("the account has no workspace, which should not happen: create one with 'afy workspaces create <name>'")
	case len(all) == 1:
		return &all[0], nil
	}
	names := make([]string, len(all))
	for i, w := range all {
		names[i] = w.Name
	}
	if !interactive {
		return nil, refuse("this account has %d workspaces (%s): pass --workspace <name> to say which. "+
			"Nothing was sent.", len(all), strings.Join(names, ", "))
	}
	fmt.Fprintln(out, "Which workspace?")
	for i, n := range names {
		fmt.Fprintf(out, "  %d) %s\n", i+1, n)
	}
	fmt.Fprintf(out, "Workspace [1-%d]: ", len(all))
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.TrimSpace(line)
	if i, err := strconv.Atoi(answer); err == nil && i >= 1 && i <= len(all) {
		return &all[i-1], nil
	}
	for i, n := range names {
		if n == answer {
			return &all[i], nil
		}
	}
	return nil, refuse("no workspace chosen (%q is not one of 1-%d or a workspace name). Nothing was sent.", answer, len(all))
}

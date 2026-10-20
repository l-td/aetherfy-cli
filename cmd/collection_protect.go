package cmd

// afy collections protect / unprotect: a collection's protection (grants
// round 1; the design is aetherfy-control-plane docs/AGENT_ACCESS_LEVELS.md).
// No agent's key may delete a protected collection, nor delete or rewrite its
// points by filter, whatever its access level; a person still can, with an
// account key. The collection is found by name through the vectors API, in the
// workspace the request is scoped to like every collections command, and the
// change goes to the control plane by its id (PATCH /collections/{id}).
//
// Protecting narrows what agents can do, so it asks nothing. Unprotecting
// widens it, so it asks "Proceed? [y/N]" first; --yes skips the question and is
// required without a terminal or with --json.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var collectionsProtectCmd = &cobra.Command{
	Use:   "protect <name>",
	Short: "Protect a collection from agents' deletes",
	Long: `Protect a collection: no agent's API key may delete it, nor delete or rewrite its
points by filter, whatever the agent's access level. Changing or deleting
points by id stays allowed for agents that may write. You can still do all of
it with your own API key, here or in the dashboard. Nothing is deleted or
changed by protecting it, and it asks nothing.`,
	Example: `  # Protect the knowledge base the agents read
  afy collections protect support-kb --workspace research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			return collectionsProtect(r, args[0], true, true, os.Stdin, true)
		})
	},
}

var collectionsUnprotectCmd = &cobra.Command{
	Use:   "unprotect <name>",
	Short: "Let agents that may delete delete a collection again",
	Long: `Turn a collection's protection off: agents at full access in its workspace may
delete it again, and agents that may write may delete or rewrite its points by
filter. Nothing is deleted now. It asks first; --yes skips the question and is
required when stdin is not a terminal and with --json.`,
	Example: `  # Turn protection off, then confirm
  afy collections unprotect support-kb --workspace research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return collectionsProtect(r, args[0], false, collectionUnprotectYes, os.Stdin, interactive)
		})
	},
}

var collectionUnprotectYes bool

func collectionsProtect(r *vecRun, name string, protect, yes bool, stdin io.Reader, interactive bool) int {
	if !protect && !yes && r.json {
		return r.fail(refuse("not changing collection '%s': --json cannot answer the confirmation. "+
			"Pass --yes to turn its protection off without the question.", name))
	}
	if !protect && !yes && !interactive {
		return r.fail(refuse("not changing collection '%s': stdin is not a terminal, so nobody can "+
			"confirm. Pass --yes to turn its protection off without the question.", name))
	}
	client, code := r.open()
	if code != 0 {
		return code
	}
	col, err := client.GetCollection(name)
	if err != nil {
		return r.fail(err)
	}
	if !protect && !yes {
		fmt.Fprintf(r.stdout, "Agents at full access in %s will be able to delete collection '%s', and agents that "+
			"may write will be able to delete or rewrite its points by filter. Nothing is deleted now.\n",
			workspaceLabel(r.client.Workspace()), name)
		fmt.Fprint(r.stderr, "Proceed? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return r.fail(refuse("collection '%s' not changed: the change was not confirmed", name))
		}
	}
	change, err := r.controlPlane().SetCollectionProtected(col.ID, protect)
	if err != nil {
		return r.fail(err)
	}
	if r.json {
		return r.printJSON(struct {
			vecEnvelope
			Collection string `json:"collection"`
			Protected  bool   `json:"protected"`
			NoOp       bool   `json:"no_op"`
		}{vecEnvelope: r.envelope(), Collection: name, Protected: change.Protected, NoOp: change.NoOp})
	}
	switch {
	case change.NoOp && protect:
		fmt.Fprintf(r.stdout, "No change: collection '%s' is already protected.\n", name)
	case change.NoOp:
		fmt.Fprintf(r.stdout, "No change: collection '%s' is not protected.\n", name)
	case protect:
		fmt.Fprintf(r.stdout, "Collection '%s' is protected: no agent's key may delete it, nor delete or rewrite its points by filter.\n", name)
	default:
		fmt.Fprintf(r.stdout, "Collection '%s' is no longer protected.\n", name)
	}
	return 0
}

func init() {
	collectionsUnprotectCmd.Flags().BoolVarP(&collectionUnprotectYes, "yes", "y", false, "Turn protection off without asking")
	collectionsCmd.AddCommand(collectionsProtectCmd)
	collectionsCmd.AddCommand(collectionsUnprotectCmd)
}

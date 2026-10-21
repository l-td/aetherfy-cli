package cmd

// afy collections protect / unprotect: a collection's protection (grants
// rounds 1 and 2; the design is aetherfy-control-plane
// docs/AGENT_ACCESS_LEVELS.md). "Protected" means no agent removes data from the
// collection: at any level, no agent's key may delete it, delete its points,
// vectors or payload fields (by id or by filter), or delete or repoint an alias
// that points at it; a person still can, with an account key. Adding, updating
// and overwriting stay allowed. The collection is found by name through the
// vectors API, in the workspace the request is scoped to like every collections
// command, and the change goes to the control plane by its id
// (PATCH /collections/{id}).
//
// Protecting narrows what agents can do, so it asks nothing and takes no
// confirmation inputs at all. Unprotecting widens it, so it asks
// "Proceed? [y/N]" through the shared confirmation (cmd/confirm.go): --yes skips
// the question and is required without a terminal (exit 2, before anything is
// sent); a "no" exits 1.

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var collectionsProtectCmd = &cobra.Command{
	Use:   "protect <name>",
	Short: "Protect a collection: no agent removes data from it",
	Long: `Protect a collection: no agent's API key may delete it, delete its points,
vectors or payload fields (by id or by filter), or delete or repoint an alias
that points at it, whatever the agent's access level. Adding and updating points
stays allowed, and so does overwriting an existing point: protection stops
removal, not change. You can still do all of it with your own API key, here or
in the dashboard. Nothing is deleted or changed by protecting it, and it asks
nothing.`,
	Example: `  # Protect the knowledge base the agents read
  afy collections protect support-kb --workspace research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVec(cmd, func(r *vecRun) int {
			return collectionsProtect(r, args[0])
		})
	},
}

var collectionsUnprotectCmd = &cobra.Command{
	Use:   "unprotect <name>",
	Short: "Let agents remove data from a collection again",
	Long: `Turn a collection's protection off: agents with read and write access in its
workspace may delete its points again, and agents with full access may delete
the collection. Nothing is deleted now. It asks first; --yes skips the question
and is required when stdin is not a terminal. Answering no exits 1 and changes
nothing.`,
	Example: `  # Turn protection off, then confirm
  afy collections unprotect support-kb --workspace research`,
	Args: refuseArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		interactive := term.IsTerminal(int(os.Stdin.Fd()))
		return runVec(cmd, func(r *vecRun) int {
			return collectionsUnprotect(r, args[0], collectionUnprotectYes, os.Stdin, interactive)
		})
	},
}

var collectionUnprotectYes bool

// collectionsProtect protects: it widens nothing, so there is nothing to ask.
func collectionsProtect(r *vecRun, name string) int {
	return changeProtection(r, name, true, nil)
}

// collectionsUnprotect turns protection off, after the confirmation.
func collectionsUnprotect(r *vecRun, name string, yes bool, stdin io.Reader, interactive bool) int {
	ask := destructiveAsk{
		action:   fmt.Sprintf("turns protection off for collection '%s'", name),
		warn:     []string{unprotectSentence(fmt.Sprintf("'%s'", name)), "Nothing is deleted now."},
		prompt:   "Proceed? [y/N] ",
		accept:   yesAnswer,
		mismatch: fmt.Sprintf("collection '%s' was not changed", name),
	}
	// Refused before anything is sent when nobody can answer.
	if err := refuseUnconfirmable(yes, interactive, ask.action, ask.skip()); err != nil {
		return r.fail(err)
	}
	return changeProtection(r, name, false, func() error {
		if yes {
			return nil
		}
		return askToConfirm(ask, vecPrompter(r, stdin))
	})
}

// changeProtection finds the collection, runs confirm (nil: nothing to confirm)
// and sends the change.
func changeProtection(r *vecRun, name string, protect bool, confirm func() error) int {
	client, code := r.open()
	if code != 0 {
		return code
	}
	col, err := client.GetCollection(name)
	if err != nil {
		return r.fail(err)
	}
	if confirm != nil {
		if err := confirm(); err != nil {
			return r.fail(err)
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
		fmt.Fprintf(r.stdout, "Collection '%s' is protected: no agent's key may delete it or remove data from it. "+
			"It takes effect within %s.\n", name, accessChangeWindow)
	default:
		fmt.Fprintf(r.stdout, "Collection '%s' is no longer protected. It takes effect within %s.\n", name, accessChangeWindow)
	}
	return 0
}

func init() {
	collectionsUnprotectCmd.Flags().BoolVarP(&collectionUnprotectYes, "yes", "y", false, "Turn protection off without asking")
	collectionsCmd.AddCommand(collectionsProtectCmd)
	collectionsCmd.AddCommand(collectionsUnprotectCmd)
}

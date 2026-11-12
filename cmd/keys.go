package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Create, list and revoke API keys",
	Long: `Create, list and revoke the API keys of your account.

Keys are created by approving a code in the dashboard, not by a command that
holds a key: a stolen key cannot make more keys. afy keys create prints a code
and a page, a person signed in to the dashboard approves the code there, and the
new key is shown once.`,
}

// --- CREATE ---

var keysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an API key by approving a code in the dashboard",
	Long: `Create an API key. The key is made when you approve a code in the dashboard,
not by this command: afy prints a code and a page, you open the page in a
browser where you are signed in to Aetherfy, check that it shows the same code
and approve it.

The new key is printed once, with the words "This is the only time the full key
is shown.", and stored nowhere: not in the credentials file and not in a log.
Copy it when it appears. To sign this machine in instead, run afy login.

This command needs no key of its own and does not use the one it has.`,
	Example: `  # Create a key for a CI system
  afy keys create --name ci

  # Print the key as JSON: {"api_key": "...", "key": {...}}
  afy keys create --name ci --json`,
	Args: cobra.NoArgs,
	RunE: runKeysCreate,
}

var (
	keysCreateName      string
	keysCreateNoBrowser bool
)

func runKeysCreate(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("name") && strings.TrimSpace(keysCreateName) == "" {
		return refuse("--name got an empty name. Name the key, or leave --name out. Nothing was sent.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := runDeviceFlow(ctx, api.NewAnonymousClient(), deviceOptions{
		purpose:   "create the key",
		again:     "afy keys create",
		keyName:   strings.TrimSpace(keysCreateName),
		noBrowser: keysCreateNoBrowser,
	})
	if err != nil {
		return err
	}

	// THE ONE PRINT OF THE PLAINTEXT, in one of two forms by mode: the JSON
	// document on stdout, or the words below on stdout. It goes to no file and
	// no log: this command stores nothing (the credentials file is not touched),
	// and nothing else here formats the key. Under --json the words are not
	// printed, because they would put the key on stderr as well.
	if output.IsJSON() {
		// The document is the one print: the words below would put the key on stderr too.
		return output.JSON(map[string]interface{}{"api_key": res.APIKey, "key": res.Key})
	}
	output.PrintSuccess("API key created")
	if res.Key != nil {
		output.KeyValue("ID", res.Key.ID)
		if res.Key.Name != "" {
			output.KeyValue("Name", res.Key.Name)
		}
	}
	output.Println("")
	output.Bold.Println("  " + res.APIKey)
	output.Println("")
	output.Println("This is the only time the full key is shown.")
	output.Dim.Println("It is stored nowhere on this machine. Copy it now.")
	return nil
}

// --- LIST ---

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the API keys of your account",
	Long: `List the API keys of your account: id, name, the first characters of the key,
when it was created and when it was last used. The full key is never shown again
after it is created.`,
	Example: `  afy keys list
  afy keys list --json`,
	Args: cobra.NoArgs,
	RunE: runKeysList,
}

func runKeysList(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}

	sp := output.NewSpinner("Fetching keys...")
	sp.Start()
	list, err := api.NewClient().ListKeys()
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to list keys", err)
	}

	// The server's document, unchanged.
	if output.IsJSON() {
		return output.JSON(list.Raw)
	}

	if len(list.Keys) == 0 {
		output.PrintInfo("No API keys.")
		return nil
	}
	table := output.Table([]string{"ID", "Name", "Key", "Created", "Last used"})
	for _, k := range list.Keys {
		name := k.Name
		if k.IsSystemManaged {
			name += " (system-managed)"
		}
		table.Append([]string{k.ID, name, k.KeyPrefix, keyTime(&k.CreatedAt), keyTime(k.LastUsedAt)})
	}
	table.Render()
	output.Println("")
	output.Dim.Printf("Total: %d key(s)\n", len(list.Keys))
	return nil
}

// keyTime spells a key row's timestamp for a person; a key never used says so.
func keyTime(iso *string) string {
	if iso == nil || *iso == "" {
		return "never"
	}
	if t, err := time.Parse(time.RFC3339, *iso); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return *iso
}

// --- REVOKE ---

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke an API key",
	Long: `Revoke an API key by its id (afy keys list shows the ids). Anything that
uses the key is refused from then on. A revoked key cannot be restored.

Asks [y/N] first; when the id is the key this CLI is using, the question says
so. --yes skips the question; it is required when stdin is not a terminal, so a
script cannot revoke by accident. Answering no exits 1: a cancelled revoke is
never a success.`,
	Example: `  afy keys revoke 3f2c9a1e-0000-0000-0000-000000000000
  afy keys revoke 3f2c9a1e-0000-0000-0000-000000000000 --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runKeysRevoke,
}

var keysRevokeYes bool

func runKeysRevoke(cmd *cobra.Command, args []string) error {
	if err := checkAuth(); err != nil {
		return err
	}
	id := strings.TrimSpace(args[0])
	if id == "" {
		return refuse("Give the id of the key to revoke ('afy keys list' shows them). Nothing was sent.")
	}

	client := api.NewClient()
	action := fmt.Sprintf("revokes API key '%s'", id)
	ask := destructiveAsk{
		action: action,
		prompt: fmt.Sprintf("Revoke API key '%s'? [y/N] ", id),
		accept: yesAnswer,
	}

	// The question names the key it is about, and says so when it is the one this
	// CLI signs in with. That takes the key list, so it is read only when there
	// is a question to ask, and after the run is known to be answerable: no
	// terminal and no --yes is refused before anything is sent.
	if !keysRevokeYes {
		if err := refuseUnconfirmable(false, stdinIsTerminal(), action, ask.skip()); err != nil {
			return err
		}
		list, err := client.ListKeys()
		if err != nil {
			return printRefusal("Failed to read the keys before asking", err)
		}
		if row, relation := currentKeyRelation(list.Keys, id, config.GetCredentials().APIKey); row != nil {
			ask.prompt = fmt.Sprintf("Revoke API key '%s' (%s, %s)? [y/N] ", id, row.Name, row.KeyPrefix)
			switch relation {
			case relationIsCurrent:
				ask.warn = []string{"This is the key this CLI is using. Revoking it ends this CLI's access: every afy command is refused until you sign in again with 'afy login'."}
			case relationMayBeCurrent:
				ask.warn = []string{"This may be the key this CLI is using (another key starts with the same characters). If it is, revoking it ends this CLI's access: every afy command is refused until you sign in again with 'afy login'."}
			}
		}
	}
	if err := confirmDestructive(keysRevokeYes, ask); err != nil {
		return err
	}

	sp := output.NewSpinner("Revoking key...")
	sp.Start()
	err := client.RevokeKey(id)
	sp.Stop()
	if err != nil {
		return printRefusal("Failed to revoke key", err)
	}

	output.PrintSuccess("API key '%s' revoked", id)
	return output.Result(map[string]interface{}{"revoked": true, "id": id})
}

const (
	relationNotCurrent = iota
	relationIsCurrent
	relationMayBeCurrent
)

// currentKeyRelation finds the row for id in the account's keys and says
// whether it is the key the CLI holds. The server shows a key only by its
// prefix ("afy_live_ab..."), so the match is that prefix against the held key:
// when another row starts the same way the answer is "may be", never a flat yes.
// row is nil when the account has no such id (the server decides what that is).
func currentKeyRelation(keys []api.APIKey, id, held string) (row *api.APIKey, relation int) {
	startsLike := func(k api.APIKey) bool {
		p := strings.TrimRight(k.KeyPrefix, ".…")
		return p != "" && held != "" && strings.HasPrefix(held, p)
	}
	matching := 0
	for i := range keys {
		if keys[i].ID == id {
			row = &keys[i]
		}
		if startsLike(keys[i]) {
			matching++
		}
	}
	switch {
	case row == nil || !startsLike(*row):
		return row, relationNotCurrent
	case matching == 1:
		return row, relationIsCurrent
	default:
		return row, relationMayBeCurrent
	}
}

func init() {
	keysCreateCmd.Flags().StringVar(&keysCreateName, "name", "", "Name for the new key")
	keysCreateCmd.Flags().BoolVar(&keysCreateNoBrowser, "no-browser", false, "do not try to open the approval page in a browser")
	keysRevokeCmd.Flags().BoolVarP(&keysRevokeYes, "yes", "y", false, "Revoke without asking")

	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysRevokeCmd)
}

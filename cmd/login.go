package cmd

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/l-td/aetherfy-cli/internal/api"
	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/output"
	"github.com/spf13/cobra"
)

// The stored-credentials path is COMPUTED, not described. It used to be
// hardcoded as "~/.aetherfy/credentials.yaml", which is wrong on Windows:
// ConfigDir() resolves to %APPDATA%\aetherfy\ on that platform, and
// $AETHERFY_CONFIG_DIR overrides everywhere. This is the sentence a user
// follows to find, inspect, or delete their credentials, so being wrong on a
// whole platform sends them hunting in a directory that does not exist.
// Asking config for the real path cannot drift from it.
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to Aetherfy",
	Long: `Sign in to Aetherfy.

With no key to hand, afy login prints a code and a page. Open the page in a
browser where you are signed in to Aetherfy, check that it shows the same code
and approve it; afy then creates a key for this machine and stores it in ` + config.CredentialsPath() + `

Keys are created by approving a code in the dashboard, not by a command that
holds a key. To sign in with a key you already have, pass it with --api-key or
pipe it on stdin, or set the AETHERFY_API_KEY environment variable instead of
signing in. Keys are listed and revoked with afy keys.`,
	Example: `  # Sign in by approving a code in the dashboard
  afy login

  # The same, without opening a browser from here
  afy login --no-browser

  # Sign in with a key you already have
  afy login --api-key afy_live_xxxxx

  # The same, from a pipe
  echo "$AFY_KEY" | afy login

  # Set environment variable (alternative)
  export AETHERFY_API_KEY=afy_live_xxxxx`,
	Args: cobra.NoArgs,
	RunE: runLogin,
}

var (
	loginAPIKey    string
	loginNoBrowser bool
)

// loginStdin is where a key piped to afy login is read from. A var so a test
// can pipe one.
var loginStdin io.Reader = os.Stdin

func init() {
	loginCmd.Flags().StringVar(&loginAPIKey, "api-key", "", "API key to use for authentication")
	loginCmd.Flags().BoolVar(&loginNoBrowser, "no-browser", false, "do not try to open the approval page in a browser")
}

func runLogin(cmd *cobra.Command, args []string) error {
	apiKey := loginAPIKey

	// A key piped on stdin is a key to sign in with. A terminal is not read: the
	// "Enter your API key" prompt is gone, and a person without a key signs in by
	// approving a code.
	if apiKey == "" && !stdinIsTerminal() {
		input, err := bufio.NewReader(loginStdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return printRefusal("Failed to read the key from stdin", err)
		}
		apiKey = strings.TrimSpace(input)
	}

	if apiKey != "" {
		return finishLogin(apiKey, nil)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := runDeviceFlow(ctx, api.NewAnonymousClient(), deviceOptions{
		purpose:   "sign in",
		again:     "afy login",
		noBrowser: loginNoBrowser,
	})
	if err != nil {
		return err
	}
	return finishLogin(res.APIKey, res.Key)
}

// finishLogin validates the key against the control plane, stores it, and says
// so. created is the row of a key the device sign-in just made (nil for a key
// the person brought): when the key cannot be stored, it names that row, since
// the plaintext is gone with this run.
func finishLogin(apiKey string, created *api.APIKey) error {
	// Validate format
	if err := config.ValidateAPIKey(apiKey); err != nil {
		return refuse("%v", err)
	}

	// Validate with API
	sp := output.NewSpinner("Validating API key...")
	sp.Start()

	client := api.NewClientWithKey(apiKey)
	userInfo, err := client.ValidateAPIKey()
	sp.Stop()

	if err != nil {
		shownErr := printRefusal("Authentication failed", err)
		if apiErr, ok := err.(*api.APIError); ok && !output.IsJSON() {
			if apiErr.StatusCode == 401 {
				output.Println("\nMake sure your API key is correct and not expired.")
				output.Println("Get a new key at: " + apiKeysPage)
			}
		}
		noteUnstoredKey(created)
		return shownErr
	}

	// Save credentials
	creds := &config.Credentials{
		APIKey: apiKey,
		UserID: userInfo.UserID,
		Email:  userInfo.Email,
	}

	if err := creds.Save(); err != nil {
		noteUnstoredKey(created)
		return printRefusal("Failed to save credentials", err)
	}

	output.PrintSuccess("Logged in successfully!")

	// Show key type
	if config.IsTestKey(apiKey) {
		output.PrintWarning("Using test API key - requests will be in test mode")
	}

	output.Println("")
	output.Println("You can now use afy commands to manage your agents.")
	output.Println("Try 'afy list' to see your agents.")
	return output.Result(map[string]interface{}{
		"logged_in": true, "user_id": userInfo.UserID, "email": userInfo.Email,
		"tier": userInfo.Tier, "key_type": keyType(apiKey),
	})
}

// noteUnstoredKey says what to do about a key the device sign-in made and this
// run could not keep: it is shown once and now unrecoverable, so it is better
// revoked than left behind.
func noteUnstoredKey(created *api.APIKey) {
	if created == nil {
		return
	}
	output.PrintNote("The key this sign-in created (%s, id %s) was not stored. Revoke it with 'afy keys revoke %s', then sign in again.",
		created.KeyPrefix, created.ID, created.ID)
}

package cmd

// The vector commands: `afy collections`, `afy index`, `afy points`. Three new
// nouns, so three new groups beside secrets/workspaces/github (see the rule in
// cmd/root.go). They talk to the vectors API, not the control plane, through
// internal/vectors, with the same stored key.
//
// Built for coding agents first: every command takes --json and prints one JSON
// object with stable field names, and exits 0 on success, 1 when a request
// failed (the API's code and message on stderr, unchanged), 2 when the input
// was refused before any request (a command line cobra cannot parse included),
// and 3 when not logged in. The human output is secondary.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/l-td/aetherfy-cli/internal/config"
	"github.com/l-td/aetherfy-cli/internal/vectors"
	"github.com/spf13/cobra"
)

const (
	exitRequestFailed = 1
	exitInputRefused  = 2
)

// The four flags every vector command takes, declared once per group as
// persistent flags, which cobra hands to each subcommand. Keep each a literal
// `group.PersistentFlags().XxxVar(&v, "name", ...)` call: docs-site's surface
// extractor reads them statically (and carries them down to the subcommands,
// the way cobra does), so a loop or a helper would hide them from the docs
// guard.
var (
	vecJSON       bool
	vecVectorsURL string
	vecAPIRegion  string
	vecWorkspace  string
)

const (
	vecJSONHelp       = "Print one JSON object with stable field names (same as --output json)"
	vecVectorsURLHelp = "Vectors API endpoint (overrides " + vectors.EnvVectorsURL + " and --api-region)"
	vecAPIRegionHelp  = "API region to connect to: us-east-1, eu-central-1 or ap-southeast-1, resolved by region discovery (or " + vectors.EnvAPIRegion + ")"
	vecWorkspaceHelp  = "Workspace the collection belongs to (default " + vectors.EnvWorkspace + ", else none; --workspace \"\" forces none)"
)

// inputError is input refused before any request was sent: exit 2.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func refuse(format string, args ...interface{}) error {
	return &inputError{msg: fmt.Sprintf(format, args...)}
}

// notLoggedIn is checkAuth's refusal, reported through fail so --json gets
// its one JSON object on stderr like every other vector-command failure.
type notLoggedIn struct{}

func (notLoggedIn) Error() string { return "Not logged in. Run 'afy login' first." }

const exitNotLoggedIn = 3

// A command line cobra cannot parse (an unknown flag, a value of the wrong
// type, the wrong number of arguments) is input refused before any request,
// so it exits 2 like the refusals the commands make themselves. cobra prints
// it as text: --json may not have been read yet when parsing stops.
func refuseArgs(rule cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := rule(cmd, args); err != nil {
			return &inputError{msg: err.Error()}
		}
		return nil
	}
}

func refuseFlag(_ *cobra.Command, err error) error { return &inputError{msg: err.Error()} }

// ExitCode is the process exit code for an error Execute returned: 2 for
// input refused before any request, 1 for anything else.
func ExitCode(err error) int {
	var in *inputError
	if errors.As(err, &in) {
		return exitInputRefused
	}
	return exitRequestFailed
}

// runVec is every vector command's RunE: the login check, then op, then the
// process exit with op's code.
func runVec(cmd *cobra.Command, op func(r *vecRun) int) error {
	return exitWith(vecMain(newVecRun(cmd), op))
}

func vecMain(r *vecRun, op func(r *vecRun) int) int {
	if !config.IsLoggedIn() {
		return r.fail(notLoggedIn{})
	}
	return op(r)
}

// vecRun is one vector command's context. connect resolves the endpoint and
// workspace and builds the client; it runs only once the input has been
// checked, so a refused input sends nothing, not even region discovery.
type vecRun struct {
	json    bool
	stdout  io.Writer
	stderr  io.Writer
	connect func() (*vectors.Client, error)
	client  *vectors.Client
}

// newVecRun builds the context from the command's flags and the environment.
func newVecRun(cmd *cobra.Command) *vecRun {
	r := &vecRun{
		json:   vecJSON || config.Get().OutputFormat == "json",
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
	r.connect = func() (*vectors.Client, error) {
		apiKey := config.GetCredentials().APIKey
		res, err := vectors.NewResolver(os.Getenv, apiKey).Resolve(vectors.Options{
			Endpoint:     vecVectorsURL,
			APIRegion:    vecAPIRegion,
			Workspace:    vecWorkspace,
			WorkspaceSet: cmd.Flags().Changed("workspace"),
		})
		if err != nil {
			return nil, err
		}
		if res.Warning != "" {
			fmt.Fprintln(r.stderr, "Warning: "+res.Warning)
		}
		return vectors.New(res.Endpoint, apiKey, res.Workspace), nil
	}
	return r
}

// open resolves once. A failure has already been reported when it returns a
// non-zero code.
func (r *vecRun) open() (*vectors.Client, int) {
	if r.client == nil {
		c, err := r.connect()
		if err != nil {
			return nil, r.fail(err)
		}
		r.client = c
	}
	return r.client, 0
}

// vecEnvelope opens every --json object: where the request went, and the
// workspace the names were scoped to (null for none).
type vecEnvelope struct {
	Endpoint  string  `json:"endpoint"`
	Workspace *string `json:"workspace"`
}

func (r *vecRun) envelope() vecEnvelope {
	env := vecEnvelope{Endpoint: r.client.Endpoint()}
	if ws := r.client.Workspace(); ws != "" {
		env.Workspace = &ws
	}
	return env
}

func (r *vecRun) printJSON(v interface{}) int {
	enc := json.NewEncoder(r.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return r.fail(err)
	}
	return 0
}

// vecErrorJSON is what --json prints on stderr for a failure. status and code
// are the API's, present only when the API answered.
type vecErrorJSON struct {
	Error struct {
		Status  int    `json:"status,omitempty"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message"`
	} `json:"error"`
}

// fail reports err on stderr and returns the exit code for it.
func (r *vecRun) fail(err error) int {
	code := exitRequestFailed
	var in *inputError
	var region *vectors.InvalidRegionError
	if errors.As(err, &in) || errors.As(err, &region) {
		code = exitInputRefused
	}
	if errors.Is(err, notLoggedIn{}) {
		code = exitNotLoggedIn
	}
	var apiErr *vectors.APIError
	isAPI := errors.As(err, &apiErr)
	if r.json {
		var out vecErrorJSON
		out.Error.Message = err.Error()
		if isAPI {
			out.Error.Status, out.Error.Code, out.Error.Message = apiErr.Status, apiErr.Code, apiErr.Message
		}
		data, _ := json.Marshal(out)
		fmt.Fprintln(r.stderr, string(data))
		return code
	}
	fmt.Fprintln(r.stderr, "Error: "+err.Error())
	return code
}

// exitWith ends a command with code, the way the deployment commands do, so
// the op functions return values a test can read.
func exitWith(code int) error {
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func init() {
	collectionsCmd.PersistentFlags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	collectionsCmd.PersistentFlags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	collectionsCmd.PersistentFlags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	collectionsCmd.PersistentFlags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	indexCmd.PersistentFlags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	indexCmd.PersistentFlags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	indexCmd.PersistentFlags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	indexCmd.PersistentFlags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	pointsCmd.PersistentFlags().BoolVar(&vecJSON, "json", false, vecJSONHelp)
	pointsCmd.PersistentFlags().StringVar(&vecVectorsURL, "vectors-url", "", vecVectorsURLHelp)
	pointsCmd.PersistentFlags().StringVar(&vecAPIRegion, "api-region", "", vecAPIRegionHelp)
	pointsCmd.PersistentFlags().StringVar(&vecWorkspace, "workspace", "", vecWorkspaceHelp)

	for _, group := range []*cobra.Command{collectionsCmd, indexCmd, pointsCmd} {
		// Inherited by every subcommand.
		group.SetFlagErrorFunc(refuseFlag)
	}
}

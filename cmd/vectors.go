package cmd

// The vector commands: `afy collections`, `afy index`, `afy points`. Three new
// nouns, so three new groups beside secrets/workspaces/github (see the rule in
// cmd/root.go). They talk to the vectors API, not the control plane, through
// internal/vectors, with the same stored key.
//
// Built for coding agents first: every command takes --json and prints one JSON
// object with stable field names, and exits 0 on success, 1 when a request
// failed (the API's code and message on stderr, unchanged), 2 when the input
// was refused before any request, and 3 when not logged in (checkAuth). The
// human output is secondary.

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

// Every vector command's own flags. Registered on EACH command with a literal
// call, never through a helper or on the group: docs-site's surface extractor
// reads `xCmd.Flags().XxxVar(&v, "name", ...)` statically and does not carry a
// group's persistent flags down to its subcommands, so a flag registered any
// other way would be one the docs guard refuses to let a page mention.
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

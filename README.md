# Aetherfy CLI

The official command-line interface for the Aetherfy platform. Deploy, manage, and monitor your AI agents with ease.

## Installation

<!-- remove-on-first-release -->
No release is tagged yet, so the install script and the release downloads have
nothing to fetch — until the first tag, use `go install` or build from source.

### Install script (Linux and macOS)

```bash
curl -fsSL https://aetherfy.com/install.sh | bash
```

Windows is not supported by this script — use the release zip below. Two
environment variables control it:

| Variable | Default | Meaning |
| --- | --- | --- |
| `AETHERFY_INSTALL_DIR` | `/usr/local/bin` | Where to put the `afy` binary |
| `AETHERFY_VERSION` | latest | Version to install; takes `0.1.0` or `v0.1.0` |

```bash
curl -fsSL https://aetherfy.com/install.sh | AETHERFY_INSTALL_DIR="$HOME/.local/bin" bash
```

### GitHub Releases

Download the archive for your platform from
[the releases page](https://github.com/l-td/aetherfy-cli/releases), extract it,
and put `afy` somewhere on your `PATH`. **On Windows this is the supported
path** — take the `.zip`; the install script above refuses to run there.

### go install

Requires Go 1.21+. This compiles from source, so it needs no release:

```bash
go install github.com/l-td/aetherfy-cli@latest
```

That puts `afy` in `$(go env GOPATH)/bin`; make sure that directory is on your
`PATH`.

### From Source

Requires Go 1.21+ (and, on Windows, Git Bash or WSL for `make` — otherwise
run the `go build` line the Makefile wraps):

```bash
git clone https://github.com/l-td/aetherfy-cli.git
cd aetherfy-cli
make install
```

`make install` puts `afy` in `$(go env GOPATH)/bin`; make sure that
directory is on your `PATH`.

## Quick Start

```bash
# Authenticate with your API key
afy login

# Scaffold an aetherfy.yaml in your project
afy init

# Deploy from the current directory
afy deploy

# Follow logs in real-time
afy logs my-agent --follow

# List deployment history and roll back if needed
afy deployments my-agent
afy rollback my-agent 3

# Run a job agent on demand and inspect its run history
afy agents run my-job --wait
afy agents runs my-job
```

## Commands

### Authentication

| Command | Description |
|---------|-------------|
| `afy login` | Authenticate with your API key (stored in `credentials.yaml` in the [config directory](#config-file), mode 0600) |
| `afy logout` | Remove stored credentials |
| `afy whoami` | Show current authentication status and account info |

### Project Initialization

| Command | Description |
|---------|-------------|
| `afy init [path]` | Scaffold an `aetherfy.yaml` by auto-detecting runtime and entrypoint |

Flags: `--name`, `--runtime`, `--entrypoint`, `--type`, `--region`, `--memory`, `--keep-alive`, `--workspace`, `--schedule`, `--force`, `--yes`.

```bash
# Scaffold a scheduled job agent non-interactively
afy init --name nightly-report --type job --schedule "0 3 * * *"
```

`--schedule` writes the `schedule:` field into `aetherfy.yaml`. When you pick
`job` at the interactive type prompt, `afy init` also asks for a schedule
(leave it blank for none). The expression is validated server-side on deploy.

### Agents

| Command | Description |
|---------|-------------|
| `afy agents list` | List all agents |
| `afy agents create <name>` | Create a new agent (`--type service\|job`, `--runtime`, `--spawn-enabled`, `--description`) |
| `afy agents delete <name>` | Delete an agent and all its deployments |
| `afy agents status <name>` | Show detailed agent status |
| `afy agents stop <name>` | Pause a running agent |
| `afy agents start <name>` | Resume a paused agent |
| `afy agents rename <current> <new>` | Rename an agent (URL stays the same) |

### Runs & Schedules

Only `type: job` agents can be run on demand or scheduled. A run is one
execution of the job: it starts, does its work, and terminates.

| Command | Description |
|---------|-------------|
| `afy agents run <name>` | Trigger a one-off run now, independent of any schedule |
| `afy agents run <name> --payload '{...}'` | Pass an inline JSON payload to the run |
| `afy agents run <name> --payload-file <file>` | Read the JSON payload from a file |
| `afy agents run <name> --wait` | Block until the run finishes (exit `0` completed, `1` failed) |
| `afy agents runs <name>` | Show run history, newest first (scheduled + manual) |
| `afy agents runs <name> --limit N` | Limit the run history (default 20, max 100) |
| `afy agents schedule pause <name>` | Stop scheduled runs from firing (manual runs still work) |
| `afy agents schedule resume <name>` | Resume a paused schedule |

```bash
# Fire a run and come back later
afy agents run nightly-report

# Run with input and gate a CI job on the result
afy agents run nightly-report --payload '{"date":"2026-07-17"}' --wait

# What ran recently, and how did it go?
afy agents runs nightly-report --limit 5

# Hold the schedule while you investigate, then let it run again
afy agents schedule pause nightly-report
afy agents schedule resume nightly-report
```

`--payload` and `--payload-file` are mutually exclusive. `--wait` polls to a
terminal state (up to 30 minutes) and exits non-zero if the run fails, which
makes it usable as a CI gate.

Pausing is idempotent, and resuming recomputes the next run from now —
occurrences that elapsed while paused are skipped, never backfilled.

Once an agent has a schedule, `afy agents list` grows `SCHEDULE`, `NEXT RUN`,
and `LAST RUN` columns, and `afy agents status <name>` shows the same three
fields. Times are UTC. `NEXT RUN` reads `(paused)` while the schedule is
paused; `LAST RUN` shows the last outcome (`fired`, `skipped`, `missed`) with
a relative timestamp, or `never`. Accounts with no scheduled agents keep the
original list layout.

### Workspaces

Workspaces group related agents so they can share secrets and vector collections.

| Command | Description |
|---------|-------------|
| `afy workspaces create <name>` | Create a workspace (3–63 chars, lowercase/hyphens) |
| `afy workspaces list` | List all workspaces with agent counts |
| `afy workspaces info <name>` | Show workspace details |
| `afy workspaces agents <name>` | List agents in a workspace |
| `afy workspaces delete <name>` | Delete an empty workspace and its secrets |

### Deployment

| Command | Description |
|---------|-------------|
| `afy deploy [path]` | Build and deploy the project (watches by default) |
| `afy deploy --detach` | Upload and return immediately without streaming |
| `afy deploy --agent <name>` | Override agent target (otherwise read from `aetherfy.yaml`) |
| `afy deploy --create` | Create the agent from `aetherfy.yaml`'s `type` and `runtime` if it does not exist |
| `afy deploy --from-github <owner/repo[@ref]>` | Deploy directly from a public GitHub repo |
| `afy deployments <agent>` | Show deployment history (newest first) |
| `afy rollback <agent> [version]` | Roll back to a previously deployed version (skips the build step) |

### Logs

| Command | Description |
|---------|-------------|
| `afy logs <agent>` | View the last 50 log lines |
| `afy logs <agent> --follow` | Stream logs in real-time |
| `afy logs <agent> --tail 200` | View last 200 lines |
| `afy logs <agent> --since 1h` | Show logs from the last hour |
| `afy logs <agent> --level ERROR,WARN` | Filter by level(s), comma-separated |
| `afy logs <agent> --stream stderr` | Filter by stream(s): `stdout`, `stderr`, `system` |
| `afy logs <agent> --run <run-id>` | Show only the logs of one specific run |

```bash
# Grab a run id from the history, then read just that run's output
afy agents runs nightly-report
afy logs nightly-report --run <run-id>
```

`--run` combines with `--follow`, `--level`, and `--stream`.

### Secrets

Secrets can be scoped to an agent or to a workspace. Agent-scoped values override workspace-scoped values with the same key.

| Command | Description |
|---------|-------------|
| `afy secrets list <agent>` | List secret keys for an agent |
| `afy secrets list --workspace <name>` | List workspace-scoped secret keys |
| `afy secrets set <agent> KEY=value [KEY2=value2 ...]` | Set one or more secrets |
| `afy secrets set <agent> KEY --stdin` | Read a secret value from stdin |
| `afy secrets set --workspace <name> KEY=value` | Set a workspace-scoped secret |
| `afy secrets delete <agent> KEY` | Delete a secret |

Keys starting with `AETHERFY_` are reserved.

### Multi-Agent (Spawn)

| Command | Description |
|---------|-------------|
| `afy spawn <parent> <child>` | Spawn a JOB agent from a SERVICE parent |
| `afy spawn <parent> <child> --payload '{...}'` | Pass a JSON payload via `AETHERFY_SPAWN_PAYLOAD` |
| `afy spawn <parent> <child> --payload-file payload.json` | Read payload from a file |
| `afy spawn <parent> <child> --stdin` | Read payload from stdin |

The parent must have `spawn.enabled: true`, and the child must be of type `job`.

**`spawn` vs `agents run`** — both start a job agent with a JSON payload, but
they are different entry points:

- `afy spawn <parent> <child>` is the parent-to-worker path: a SERVICE agent
  dispatches work to one of its declared workers. It requires a parent, and
  the resulting runs belong to the parent's history.
- `afy agents run <name>` is a manual **root** run of a job agent — no parent
  involved. These runs appear in `afy agents runs <name>` alongside the
  scheduled ones.

Note the flags differ: `spawn` accepts `--stdin`, `agents run` does not;
`agents run` accepts `--wait`, `spawn` does not.

### GitHub Integration

Connect your GitHub account to deploy on every push.

| Command | Description |
|---------|-------------|
| `afy github connect` | Install the Aetherfy GitHub App |
| `afy github disconnect` | Remove the GitHub App installation |
| `afy github status` | Show connection status |
| `afy github link <agent> <owner/repo[@branch]>` | Link an agent to a repo (default branch: `main`). Add `--root-dir <path>` when several agents share one repository — only that folder is uploaded on a push |
| `afy github unlink <agent>` | Remove the webhook link |

### Utilities

| Command | Description |
|---------|-------------|
| `afy version` | Print version, build date, and commit hash |
| `afy completion [bash\|zsh\|fish\|powershell]` | Generate shell completion script |

`completion` writes the script to stdout; wire it into your shell:

```bash
# bash
source <(afy completion bash)

# zsh
source <(afy completion zsh)

# fish
afy completion fish | source
```

```powershell
# PowerShell
afy completion powershell | Out-String | Invoke-Expression
```

To make it permanent: append the bash or zsh line to `~/.bashrc` or `~/.zshrc`;
for fish, write the script once with `afy completion fish >
~/.config/fish/completions/afy.fish`; for PowerShell, append `afy completion
powershell >> $PROFILE`. `afy completion --help` prints the same instructions —
it is the source of truth if this ever disagrees.

## Configuration

### Config File

The CLI resolves its config directory in this order:

1. `$AETHERFY_CONFIG_DIR`, if set.
2. On Windows, `%APPDATA%\aetherfy`.
3. On Linux, `$XDG_CONFIG_HOME/aetherfy`, if `XDG_CONFIG_HOME` is set.
4. Otherwise, `~/.aetherfy`.

`afy whoami` prints the resolved credentials path if you need the concrete one.

The CLI stores configuration in `config.yaml` inside that directory:

```yaml
api_url: https://agents.aetherfy.com/api/v1
default_region: us-east-1
output_format: text
no_color: false
verbose: false
```

Credentials are stored separately in `credentials.yaml`, in that same resolved
config directory, with permissions `0600`.

`--config` selects the config *file* only; `credentials.yaml` always resolves
from the config directory — set `AETHERFY_CONFIG_DIR` to relocate both.

### Environment Variables

| Variable | Description |
|----------|-------------|
| `AETHERFY_API_KEY` | API key (overrides stored credentials) |
| `AETHERFY_API_URL` | API base URL (overrides config) |
| `AETHERFY_CONFIG_DIR` | Overrides the config directory (see resolution order above) |
| `NO_COLOR` | Disable colored output |
| `XDG_CONFIG_HOME` | Used on Linux if set |

### Global Flags

| Flag | Description |
|------|-------------|
| `--config` | Config file path |
| `--api-url` | API base URL |
| `--output, -o` | Output format: `text`, `json`, `table` |
| `--verbose, -v` | Verbose output |
| `--no-color` | Disable colors |

### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Error — usage errors and failed operations |
| `3` | Not authenticated — any command requiring auth, including `afy whoami` when not logged in |

## Project Structure

Your agent project must include an `aetherfy.yaml` at the root:

```yaml
name: my-agent
runtime: python3.11         # python3.11, python3.12, python3.13, node20, node22, node20-ts, node22-ts, bun, dockerfile
type: service               # service or job
entrypoint: main.py         # optional — auto-detected by `afy init`
regions:                    # optional — us-east-1, eu-central-1, ap-southeast-1
  - us-east-1
memory_mb: 512              # 256, 512, or 1024
keep_alive: false           # always-on billing

# Optional: cron schedule — job agents only
schedule: "0 3 * * *"

# Optional: attach to a workspace to share secrets and vector collections
workspace: invoice-pipeline

# Optional: enable multi-agent spawning
spawn:
  enabled: true
  workspace: invoice-pipeline
  workers:
    - classifier
    - summarizer
```

Required fields: `name`, `runtime`. Run `afy init` to scaffold a valid file.

`schedule` is a 5-field cron expression, evaluated in **UTC**, with a minimum
interval of every 5 minutes, and is valid only for `type: job` agents. Omit
the field on a re-deploy to keep the existing schedule; set `schedule: null`
to clear it.

### .afyignore

Create a `.afyignore` file to exclude files from deployment:

```
# .afyignore
.git
.env
__pycache__
*.pyc
node_modules
venv
```

Common patterns (`.git`, `.env`, `__pycache__`, `node_modules`, `venv`, `.DS_Store`, `*.log`, …) are ignored by default.

## Development

### Building

```bash
# Build for current platform
make build

# Build for all platforms
make build-all

# Run tests
make test

# Install locally
make install
```

### Control-plane error codes

The CLI branches on the error-code strings the control plane sends — the deploy
prompts, the lifecycle retry and the run-now paths each key on one. Those
strings are a contract, and a rename on the server side would silently turn a
branch here into dead code: the tests mock the server, so they would keep
agreeing with themselves. (No codes are spelled out here on purpose. Prose
naming one is one more place to go stale, and nothing checks prose.)

`test/cp-error-codes-snapshot.json` is the control plane's registry, committed
so CI can check against it without a checkout of that repo, and
`test/cp_error_codes_test.go` requires every code-shaped literal in this
repository to be either a code that registry holds or an allowlisted non-code.
When the control plane adds or changes a code:

```bash
# needs ../aetherfy-control-plane checked out (or set AETHERFY_CP_ROOT)
make cp-error-snapshot
```

Where that checkout is present, `make test` re-runs the extraction and fails on
any difference, so the committed snapshot cannot quietly go stale.

### Requirements

- Go 1.21+
- Make (optional)

## Support

- Documentation: https://docs.aetherfy.com
- Issues: https://github.com/l-td/aetherfy-cli/issues
- Email: support@aetherfy.com

## License

Apache 2.0 — See [LICENSE](LICENSE) for details.

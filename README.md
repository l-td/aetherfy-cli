# Aetherfy CLI

The official command-line interface for the Aetherfy platform. Deploy, manage, and monitor your AI agents with ease.

## Installation

### Quick Install (Linux/macOS)

```bash
curl -fsSL https://aetherfy.run/install.sh | bash
```

### Homebrew (macOS)

```bash
brew install aetherfy/tap/afy
```

### From Source

Requires Go 1.21+:

```bash
git clone https://github.com/aetherfy/cli.git
cd cli
make install
```

### Windows

Download the latest release from [GitHub Releases](https://github.com/aetherfy/cli/releases).

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
afy rollback my-agent v3
```

## Commands

### Authentication

| Command | Description |
|---------|-------------|
| `afy login` | Authenticate with your API key (stored in `~/.aetherfy/credentials.yaml`, mode 0600) |
| `afy logout` | Remove stored credentials |
| `afy whoami` | Show current authentication status and account info |

### Project Initialization

| Command | Description |
|---------|-------------|
| `afy init [path]` | Scaffold an `aetherfy.yaml` by auto-detecting runtime and entrypoint |

Flags: `--name`, `--runtime`, `--entrypoint`, `--type`, `--region`, `--memory`, `--keep-alive`, `--workspace`, `--force`.

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

### GitHub Integration

Connect your GitHub account to deploy on every push.

| Command | Description |
|---------|-------------|
| `afy github connect` | Install the Aetherfy GitHub App |
| `afy github disconnect` | Remove the GitHub App installation |
| `afy github status` | Show connection status |
| `afy github link <agent> <owner/repo[@branch]>` | Link an agent to a repo (default branch: `main`) |
| `afy github unlink <agent>` | Remove the webhook link |

### Utilities

| Command | Description |
|---------|-------------|
| `afy version` | Print version, build date, and commit hash |
| `afy completion [bash\|zsh\|fish\|powershell]` | Generate shell completion script |

## Configuration

### Config File

The CLI stores configuration in `~/.aetherfy/config.yaml`:

```yaml
api_url: https://agents.aetherfy.com/api/v1
default_region: iad
output_format: text
no_color: false
verbose: false
```

Credentials are stored separately in `~/.aetherfy/credentials.yaml` with permissions `0600`.

### Environment Variables

| Variable | Description |
|----------|-------------|
| `AETHERFY_API_KEY` | API key (overrides stored credentials) |
| `AETHERFY_API_URL` | API base URL (overrides config) |
| `AETHERFY_CONFIG_DIR` | Config directory path (defaults to `~/.aetherfy`) |
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

## Project Structure

Your agent project must include an `aetherfy.yaml` at the root:

```yaml
name: my-agent
runtime: python3.11         # python3.11, python3.12, python3.13, node20, node22, node20-ts, node22-ts, bun, dockerfile
type: service               # service or job
entrypoint: main.py         # optional — auto-detected by `afy init`
regions:                    # optional — list of iad, fra, sin
  - iad
memory_mb: 512              # 256, 512, or 1024
keep_alive: false           # always-on billing

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

### Requirements

- Go 1.21+
- Make (optional)

## Support

- Documentation: https://docs.aetherfy.run
- Issues: https://github.com/aetherfy/cli/issues
- Email: support@aetherfy.run

## License

Apache 2.0 — See [LICENSE](LICENSE) for details.

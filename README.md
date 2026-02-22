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

# List your agents
afy agents list

# Create a new agent
afy agents create my-agent

# Deploy your code
afy deploy ./my-agent

# View logs
afy logs my-agent

# Spawn a JOB agent
afy spawn worker-agent --payload '{"task": "process"}'
```

## Commands

### Authentication

| Command | Description |
|---------|-------------|
| `afy login` | Authenticate with your API key |
| `afy logout` | Remove stored credentials |
| `afy whoami` | Show current authentication status |

### Agents

| Command | Description |
|---------|-------------|
| `afy agents list` | List all agents |
| `afy agents create <name>` | Create a new agent |
| `afy agents delete <name>` | Delete an agent |
| `afy agents status <name>` | Show agent status |

### Deployment

| Command | Description |
|---------|-------------|
| `afy deploy [path]` | Deploy code to an agent |
| `afy deploy --watch` | Deploy and stream logs |

### Logs

| Command | Description |
|---------|-------------|
| `afy logs <agent>` | View agent logs |
| `afy logs <agent> --follow` | Stream logs in real-time |
| `afy logs <agent> --tail 100` | View last 100 lines |

### Secrets

| Command | Description |
|---------|-------------|
| `afy secrets list <agent>` | List secret keys |
| `afy secrets set <agent> KEY=value` | Set a secret |
| `afy secrets delete <agent> KEY` | Delete a secret |

### Multi-Agent

| Command | Description |
|---------|-------------|
| `afy spawn <agent>` | Spawn a JOB agent |
| `afy spawn <agent> --payload '{...}'` | Spawn with payload |

## Configuration

### Config File

The CLI stores configuration in `~/.aetherfy/config.yaml`:

```yaml
api_url: https://agents.aetherfy.com/api/v1
default_region: iad
output_format: text
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `AETHERFY_API_KEY` | API key (overrides stored credentials) |
| `AETHERFY_API_URL` | API base URL |
| `AETHERFY_CONFIG_DIR` | Config directory path |
| `NO_COLOR` | Disable colored output |

### Global Flags

| Flag | Description |
|------|-------------|
| `--config` | Config file path |
| `--api-url` | API base URL |
| `--output, -o` | Output format: text, json, table |
| `--verbose, -v` | Verbose output |
| `--no-color` | Disable colors |

## Project Structure

Your agent project should include an `aetherfy.yaml` configuration file:

```yaml
# aetherfy.yaml
name: my-agent
runtime: python3.11
entrypoint: main.py

# Optional
agent_type: SERVICE  # or JOB
spawn_enabled: true
memory_mb: 256
```

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

Apache 2.0 - See [LICENSE](LICENSE) for details.

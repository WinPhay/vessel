# Vessel

[![CI](https://github.com/WinPhay/vessel/actions/workflows/ci.yml/badge.svg)](https://github.com/WinPhay/vessel/actions/workflows/ci.yml)
[![Publish Agent Images](https://github.com/WinPhay/vessel/actions/workflows/docker.yml/badge.svg)](https://github.com/WinPhay/vessel/actions/workflows/docker.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

> **The local, time-traveling sandbox engine for autonomous AI agents.**

Vessel creates an isolated, disposable execution environment for AI coding agents. Run autonomous agents locally with zero fear of host file destruction, leaked `.env` credentials, or corrupted system packages.

---

## Agent Instructions
If you are an AI assistant or coding agent working in this repository, see [`AGENTS.md`](AGENTS.md) for architectural boundaries, verified commands, and contribution invariants.

---

## Key Features
- **Host Shield**: Executes agent commands inside an isolated container with only `/workspace` mounted.
- **Headless Browser Sandbox**: Built-in containerized Chromium subsystem (`vessel_browse_url`, `vessel_screenshot`, `vessel_extract_page`) for safe web browsing, automated testing, and visual screenshots without exposing host cookies.
- **Resource Quotas**: Enforces kernel cgroups limits on RAM (`--memory 2g`) and CPU cores (`--cpus 1.5`) to prevent host starvation.
- **Network Isolation**: Supports complete airgap mode (`--network none`) to block LLM credential exfiltration.
- **Time-Travel Rollbacks**: Create instant filesystem checkpoints (`vessel_checkpoint`) and restore them (`vessel_rollback`) in sub-second time.
- **Native MCP Interface**: Plugs directly into Claude Desktop, Cursor, Antigravity, and Cline over stdio Model Context Protocol.
- **Ephemeral Service Mesh**: Run isolated database sidecars (PostgreSQL, Redis, MongoDB) on an internal session network (`vessel_spawn_service`, `--service db=postgres`) with optional localhost ingress (`--service-port 5432:5432`) strictly bound to loopback `127.0.0.1`.
- **Process Wait & Exit Synchronization**: Eliminate agent polling loops with exit synchronization (`vessel_process_wait`) and bounded timeouts for background compilation and test tasks.
- **Checkpoint Delta Diff Engine**: Zero-dependency Myers diff generator comparing checkpoints or live workspaces (`vessel checkpoint diff`, `vessel_checkpoint_diff`) with file status metrics and token estimation.
- **Workspace Access Policy**: Run in strict read-only audit mode (`--read-only`) or protect sensitive files and wildcards (`--ro-path .env`, `--deny-path .secrets`) enforced across both Docker kernel mounts and MCP tool calls.
- **Git Worktree Shield & Workspace Review Gate**: Protects the developer's primary working tree from rogue agent overwrites by mounting an isolated Git worktree (`--worktree`). Agents can stage, diff, and commit changes on an isolated session branch (`vessel_worktree_status`, `vessel_worktree_diff`, `vessel_worktree_commit`) without polluting the host working copy.
- **Secret Redaction & DLP Vault**: Automatic egress redaction for OpenAI, Anthropic, AWS, GitHub, GCP, Slack, private keys, database URLs, and high-entropy secrets. Inject real credentials (`--secret KEY=VALUE`) for authenticated agent tasks with zero exfiltration risk to the LLM.
- **Token Scrubber**: Strips ANSI escape codes, eliminates interactive terminal hangs, and formats structured execution receipts.

---

## Quick Start

### Installation (One-Liner)
Install the latest pre-compiled static binary for your OS and CPU architecture (Linux/macOS/Windows, amd64/arm64):
```bash
curl -fsSL https://raw.githubusercontent.com/WinPhay/vessel/main/scripts/install.sh | bash
```

### Pre-Built AgentOS Images (GHCR)
Vessel publishes official multi-architecture (`linux/amd64`, `linux/arm64`) agent images to GitHub Container Registry:
```bash
# Pull official pre-baked images
docker pull ghcr.io/winphay/vessel-agent:alpine   # Lightweight base (default)
docker pull ghcr.io/winphay/vessel-agent:ubuntu   # Full developer toolchain
docker pull ghcr.io/winphay/vessel-agent:browser  # Headless Chromium agent

# Or build locally using scripts/build-images.sh
./scripts/build-images.sh all
```

### Build from Source
```bash
go build -o bin/vessel ./cmd/vessel
```

### Run Tests
```bash
go test -v ./...
```

### CLI Commands
```bash
# Execute isolated command inside sandbox (supports --network, --memory, --cpus, and --worktree)
./bin/vessel exec --workspace /path/to/project --memory 2g --cpus 1.5 --network none "npm test"

# Execute inside an ephemeral Git worktree (zero host branch pollution)
./bin/vessel exec --workspace /path/to/project --worktree "npm test"

# Inspect active worktrees and changes
./bin/vessel worktree status --workspace /path/to/project
./bin/vessel worktree diff --workspace /path/to/project

# Create a named filesystem snapshot
./bin/vessel checkpoint --workspace /path/to/project my-checkpoint

# List active checkpoints
./bin/vessel checkpoint list --workspace /path/to/project

# Compare checkpoint against live workspace or between tags
./bin/vessel checkpoint diff my-checkpoint --workspace /path/to/project

# Rollback workspace to an exact checkpoint
./bin/vessel rollback --workspace /path/to/project my-checkpoint

# Run command in read-only audit mode with protected paths
./bin/vessel exec --workspace /path/to/project --ro-path .env --read-only "npm test"

# Check engine health, Docker connectivity, and snapshots
./bin/vessel status --workspace /path/to/project
```

### Connect to Claude Desktop or Cursor (MCP Config)
```json
{
  "mcpServers": {
    "vessel": {
      "command": "/path/to/bin/vessel",
      "args": [
        "mcp",
        "--workspace", "/path/to/your/project",
        "--memory", "2g",
        "--cpus", "2.0"
      ]
    }
  }
}
```

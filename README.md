# Vessel

[![CI](https://github.com/WinPhay/vessel/actions/workflows/ci.yml/badge.svg)](https://github.com/WinPhay/vessel/actions/workflows/ci.yml)
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
- **Token Scrubber**: Strips ANSI escape codes, eliminates interactive terminal hangs, and formats structured execution receipts.

---

## Quick Start

### Installation (One-Liner)
Install the latest pre-compiled static binary for your OS and CPU architecture (Linux/macOS/Windows, amd64/arm64):
```bash
curl -fsSL https://raw.githubusercontent.com/WinPhay/vessel/main/scripts/install.sh | bash
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
# Execute isolated command inside sandbox (supports --network, --memory, and --cpus)
./bin/vessel exec --workspace /path/to/project --memory 2g --cpus 1.5 --network none "npm test"

# Create a named filesystem snapshot
./bin/vessel checkpoint my-checkpoint --workspace /path/to/project

# List active checkpoints
./bin/vessel checkpoint list --workspace /path/to/project

# Rollback workspace to an exact checkpoint
./bin/vessel rollback my-checkpoint --workspace /path/to/project

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

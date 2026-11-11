# Vessel

> **The local, time-traveling sandbox engine for autonomous AI agents.**

Vessel creates an isolated, disposable execution environment for AI coding agents. Run autonomous agents locally with zero fear of host file destruction, leaked `.env` credentials, or corrupted system packages.

---

## Agent Instructions
If you are an AI assistant or coding agent working in this repository, see [`AGENTS.md`](AGENTS.md) for architectural boundaries, verified commands, and contribution invariants.

---

## Key Features
- **Host Shield**: Executes agent commands inside an isolated container with only `/workspace` mounted.
- **Time-Travel Rollbacks**: Create instant filesystem checkpoints (`vessel_checkpoint`) and restore them (`vessel_rollback`) in sub-second time.
- **Native MCP Interface**: Plugs directly into Claude Desktop, Cursor, Antigravity, and Cline over stdio Model Context Protocol.
- **Token Scrubber**: Strips ANSI escape codes, eliminates interactive terminal hangs, and formats structured execution receipts.

---

## Quick Start

### Build
```bash
go build -o bin/vessel ./cmd/vessel
```

### Run Tests
```bash
go test -v ./...
```

### Connect to Claude Desktop or Cursor (MCP Config)
```json
{
  "mcpServers": {
    "vessel": {
      "command": "/path/to/bin/vessel",
      "args": ["mcp", "--workspace", "/path/to/your/project"]
    }
  }
}
```

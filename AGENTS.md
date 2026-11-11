# AGENTS.md — vessel

> Auto-loaded by agentic tools (opencode, Gemini CLI/agy, Codex, Copilot). Closest AGENTS.md wins; this is the project-root contract.

## Overview
- **Vessel**: The local, time-traveling sandbox engine for autonomous AI agents.
- Isolates untrusted agent execution into an ephemeral Docker container / MicroVM with instant snapshot rollback and token-optimized stdout filtering.
- Exposes native Model Context Protocol (MCP) server over stdio for Claude Desktop, Cursor, Antigravity, and Cline.
- Single source of truth for agents: this file + `DESIGN.md` + `doc/adr/` (decision history).
- Tier: Personal (Tier 2 / WinPhay).

## Map
- `cmd/vessel/` — CLI entrypoint (`vessel mcp`, `vessel exec`, `vessel status`).
- `internal/mcp/` — Model Context Protocol stdio server implementation.
- `internal/sandbox/` — Execution engine interface, Docker runtime driver, checkpoint/rollback manager.
- `internal/tokenfilter/` — Terminal output sanitizer (strips ANSI escapes, formats JSON receipts, estimates tokens).
- `configs/` — Default AgentOS container configurations and Dockerfiles.
- `DESIGN.md` — Architecture invariants, components, and non-goals.
- `doc/adr/` — Immutable architectural decision records (MADR format). Append-only.
- `.opencode/` — Agent permissions, reviewer subagent, and workflow commands.

## Commands (exact; verified 2026-11-11)
- Build: `go build -o bin/vessel ./cmd/vessel` (exit 0)
- Test: `go test -v ./...` (exit 0)
- Vet / Lint: `go vet ./...` (exit 0)
- Format: `go fmt ./...` (exit 0)
- Run CLI: `go run ./cmd/vessel --help`
- Run MCP Server: `go run ./cmd/vessel mcp`

> Go 1.27+ toolchain. Dependencies managed via Go modules (`go.mod`). Never invent commands; inspect `go.mod` and tests first.

## Git Workflow
- Branching: Trunk-based on `main`. Feature branches `feat/*`, bugfix branches `fix/*`.
- Identity: Git commit author must strictly be `WinPhay <winphay.1995@gmail.com>`.
- Commit style: Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `chore:`), imperative subject.
- Pre-commit gate: `go fmt ./...`, `go vet ./...`, and `go test ./...` must all exit 0 before commit.
- Never force-push (`git push --force`). Pushes require explicit operator approval.

## Boundaries
Always
- Verify `go test ./...` and `go vet ./...` pass with exit code 0 before declaring tasks done.
- Isolate execution environments so host filesystem files outside `/workspace` can never be mutated.
- Record structural architectural decisions as new ADRs under `doc/adr/`.
- Fix failures; never remove or weaken tests to pass gates.

Ask first
- Adding new external Go dependencies to `go.mod`.
- Modifying MCP tool schemas, tool names, or protocol contracts.
- Editing scaffolding files under `.opencode/`, `AGENTS.md`, or `DESIGN.md`.
- Executing git pushes to remote repositories.

Never
- Run `git push --force`, `rm -rf`, or destructive history rewrites.
- Commit, log, or hardcode API keys, credentials, or private secrets.
- Break the zero-host-pollution invariant (never execute untrusted agent commands on the host directly).

## Verify
- Run and report exit codes for: `go fmt ./...`, `go vet ./...`, `go test ./...`. All must be 0.
- State verified command outputs explicitly with exit codes.

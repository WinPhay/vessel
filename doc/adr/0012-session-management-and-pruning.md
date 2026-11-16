# ADR 0012: Session Management and Pruning Subsystem

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
With the introduction of ADR-0011 (Session Persistence and Reconnect Engine), Vessel execution containers, internal Docker networks (`vessel-net-<session_id>`), sidecar services (e.g. databases), and Git worktrees survive CLI disconnects and restarts when `--persist` or `--session <id>` is specified.

While this solves the operational latency of restarting environments, persistent sessions accumulate on the developer workstation over time:
1. **Resource & Container Leaks**: Stopped or dormant containers and sidecars consume disk and Docker daemon table space.
2. **Orphaned Networks**: Bridge networks created for sidecar communication remain registered even after sessions are abandoned.
3. **Worktree Footprint**: Isolated worktrees under `.vessel/worktrees/` retain checkouts and branch refs on the host repository.
4. **Visibility Vacuum**: Developers lacked a unified CLI interface to audit active/persistent sessions, inspect their network topology, or safely prune stale session state.

## Decision Outcome
Implement the Session Management and Pruning Subsystem (`internal/sandbox/session.go` and `vessel session` CLI command):

1. **Session Inspection & Discovery (`internal/sandbox/session.go`)**:
   - `ListSessions`: Discovers all Vessel-managed containers via Docker label `vessel.managed=true`, correlates running sidecars and active worktrees, and returns structured `SessionSummary` records.
   - `InspectSession`: Queries deep container, networking, IP address, and sidecar topology for a specific session ID.
   - `StopSession`: Gracefully stops the primary sandbox container without purging disk state or worktree isolation.
   - `PruneSessions`: Deterministically garbage-collects stopped (or all, via `--all`) session containers, associated sidecars, unreferenced `vessel-net-*` bridge networks, and ephemeral worktrees.

2. **Unified CLI Command (`cmd/vessel/main.go`)**:
   - `vessel session list`: Displays formatted table of session IDs, statuses (running/exited), image, container ID, attached sidecars, and worktree status. Supports `--workspace <dir>` filter and `--json`.
   - `vessel session inspect <id>`: Emits full JSON metadata for the targeted session.
   - `vessel session stop <id>`: Sends graceful SIGTERM/stop to the targeted session container.
   - `vessel session prune [--all] [--workspace <dir>]`: Cleans up dormant resources and reports reclaimed items.

3. **Safety & Non-Destructive Invariants**:
   - Never touches or prunes third-party Docker containers or networks lacking `vessel.managed=true` and `vessel.session_id`.
   - By default, `prune` skips active, running sessions to protect live agent workflows unless `--all` / `--force` is explicitly passed.
   - Host Git primary branches remain pristine; only `.vessel/worktrees/vessel-<id>` directories and `vessel/session-<id>` branches are pruned.

## Positive Consequences
- **Automated Hygiene**: One-line command (`vessel session prune`) completely purges orphaned Docker resources and worktree checkouts.
- **Operator Visibility**: Developers can observe which agent sessions are running or persistent across multiple terminal windows or MCP clients.
- **Workspace-Scoped Audit**: Teams can audit and clean up sessions scoped strictly to the current working repository.

## Invariants Maintained
- **Host-Isolation Invariant**: Pruning actions strictly operate within `.vessel/` and labeled Docker resources.
- **Contract Stability**: MCP 22-tool interface remains untouched; session lifecycle management is surfaced cleanly via CLI.

# ADR 0011: Session Persistence and Sandbox Reconnection Engine

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
Prior to this decision, every Vessel execution session (`vessel exec` or `vessel mcp`) created an ephemeral container, service mesh network, and Git worktree stamped with a high-resolution timestamp (`vessel-sandbox-<timestamp>`). Upon session exit or client disconnect, the teardown lifecycle unconditionally deleted:
1. The execution container (`ContainerRemove -f`), discarding installed packages, OS dependencies, and build caches.
2. The ephemeral service mesh (`vessel-net-<timestamp>` and sidecar containers like PostgreSQL, Redis).
3. The Git worktree and isolated session branch (`vessel/session-<timestamp>`).

In agentic workflows (such as Claude Desktop, Cursor, or long-running iterative CLI tasks), agent clients periodically restart, disconnect from stdio, or run in multi-command pipelines. Rebuilding the sandbox environment, re-cloning dependencies, re-seeding sidecar databases, and re-initializing worktrees on every restart introduces severe operational latency and destroys agent task context.

## Decision Outcome
Implement the Session Persistence and Reconnection Engine across Vessel's core subsystems:

1. **Deterministic Session Identity & Reconnect Protocol (`internal/sandbox/docker.go`)**:
   - Introduce `SessionID` and `Persistent` parameters in `DockerSandboxConfig`.
   - When `SessionID` is specified, containers are labeled with `vessel.session_id=<id>` and named `vessel-sandbox-<id>`.
   - Before creating a new container, Vessel inspects Docker for an existing container bearing that session ID.
   - If an existing container is found:
     - If stopped, it is restarted via `ContainerStart`.
     - If running, it is re-attached immediately with zero container recreation latency.
     - The sandbox records `reconnected = true`.
   - If no container is found, a new container is created and stamped with session labels.

2. **Persistent Service Mesh Re-attachment (`internal/sandbox/service.go`)**:
   - `NewServiceManager` checks if `vessel-net-<session_id>` already exists.
   - If existing, Vessel inspects the network, discovers running sidecars tagged with `vessel.session_id`, and restores the `ServiceManager.services` map without restarting containers.
   - If not existing, it provisions the network.
   - The workspace container is attached to the network if not already connected.

3. **Git Worktree Re-binding (`internal/sandbox/worktree.go`)**:
   - `NewWorktreeManager` checks if `.vessel/worktrees/vessel-<session_id>` exists and is a valid Git worktree.
   - If present, it re-binds to the existing worktree and branch without attempting redundant `git worktree add` invocations.
   - If the branch exists without the directory, it attaches the existing branch.

4. **Guarded Teardown Lifecycle**:
   - When `Persistent` is true, `DockerSandbox.Close()`, `ServiceManager.Close()`, and `WorktreeManager.Close()` cleanly stop in-memory I/O and process listeners but **preserve** the Docker container, sidecars, network, and worktree filesystem for subsequent reconnects.
   - When `Persistent` is false (default), standard disposable teardown proceeds as before.

5. **CLI & MCP Tool Surface**:
   - CLI flags on `vessel exec` and `vessel mcp`:
     - `--session <id>`: Specifies a stable session identifier.
     - `--persist`: Prevents container, worktree, and service teardown on exit.
   - MCP `vessel_status`: Reports `session_id`, `persistent`, and `reconnected` metadata when session tracking is active.

## Positive Consequences
- **Zero-Latency Agent Resume**: External AI agents and IDEs can disconnect and reconnect in milliseconds without re-installing environment dependencies.
- **Sidecar Continuity**: Ephemeral databases and mock servers stay online and retain seeded data across multiple agent execution steps.
- **Preserved Worktree Context**: Agents can resume unfinished work on isolated branches across CLI restarts.

## Invariants Maintained
- **Host-Isolation Invariant**: Persistent containers remain strictly isolated; host filesystems outside `/workspace` are never mounted.
- **Clean Branch Invariant**: Git worktrees remain isolated on `vessel/session-<id>` branches until explicitly committed or merged.
- **Zero-Leak Invariant**: Dynamic secret vault and token redactor apply identically to reconnected sessions.

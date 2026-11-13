# ADR 0006: PTY and Background Process Lifecycle Engine for MCP

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-13

## Context & Problem Statement
Prior to this decision, Vessel's execution interface (`vessel_exec`) was strictly synchronous and non-interactive (`Tty: false`, stdin closed, hard timeout).
This created two critical bottlenecks for autonomous AI agents:
1. **Interactive Prompt Hangs**: Commands requiring interactive user input (e.g. Python REPLs, Node CLI prompts, package initializers like `npm init`, confirmation questions) immediately failed or hung indefinitely.
2. **Long-Running Daemons & Servers**: Web applications, test runners in watch mode, and long-lived processes (e.g. `npm run dev`, `uvicorn`, `cargo watch`) could not be managed because `vessel_exec` blocks until process exit or timeout.

Agents need the ability to spawn background processes inside the container jail, send keystrokes to stdin, poll unread token-sanitized terminal output, and gracefully terminate processes.

## Decision Outcome
Introduce a PTY-enabled Background Process Lifecycle Manager into Vessel:

1. **Process Manager Engine (`internal/sandbox/process.go`)**:
   - `ProcessManager`: Thread-safe registry mapping `process_id` -> `ManagedProcess`.
   - `RingBuffer`: Fixed-capacity circular byte buffer (default: 256 KB) capturing stdout/stderr without unbounded memory growth.
   - PTY Terminal Support: Docker `ContainerExecCreate` with `Tty: true` and terminal window resize support (`rows`, `cols`).
   - Stdin Channel: Live bidirectional `io.WriteCloser` stream allowing input delivery into running processes.
   - Lifecycle Watcher: Background goroutine monitoring process termination, capturing exit codes, and closing streams.

2. **4 Native MCP Tools Added to MCP Surface**:
   - `vessel_start_process`: Spawns a background process inside the container jail, returning `process_id` and initial status. Accepts optional `pty` (bool, default true), `rows`, and `cols`.
   - `vessel_process_output`: Drains unread stdout/stderr from the process ring buffer up to `max_bytes`, returning token-sanitized output, `running` status, and `exit_code`.
   - `vessel_process_write`: Writes raw text or keystrokes (e.g. `\n`, `y\n`, commands) directly to the process stdin.
   - `vessel_kill_process`: Sends termination signal (`SIGTERM`, `SIGKILL`) and cleans up process resources.

## Positive Consequences
- **Interactive Agility**: Agents can drive interactive REPLs and answer CLI prompts inside the sandbox without hanging.
- **Daemon Supervision**: Dev servers and background background test watchers can run asynchronously while the agent proceeds with other tasks.
- **Token Protection**: Reading output incrementally via `vessel_process_output` prevents context explosion by delivering only unread incremental tokens.

## Invariants Maintained
- **Host Isolation**: All processes run strictly within the container `/workspace` namespace.
- **Resource Limits**: Background processes remain constrained by cgroups memory and CPU quotas established at sandbox startup.
- **Zero Memory Leaks**: Process ring buffers are bounded, and closing the sandbox terminates all background processes.

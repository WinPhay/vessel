# ADR 0016: Process Wait and Exit Synchronization Engine

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
In ADR-0006, Vessel implemented the PTY Background Process Manager (`vessel_start_process`, `vessel_process_output`, `vessel_process_write`, `vessel_kill_process`), enabling interactive and asynchronous command execution inside the container sandbox.

However, when AI agents spawn build, compilation, or test processes (e.g. `npm run build`, `cargo test`, `go test ./...`), they encountered a severe operational tax:
1. **The Polling Tax**: Because `vessel_process_output` is non-blocking, agents had to query it repeatedly across multiple turns to determine when a command completed. This consumed excessive roundtrips, context window tokens, and API latency.
2. **Missing Synchronization Primitive**: The MCP surface lacked an exit-coordination primitive allowing an agent to wait for process completion with bounded timeout.

## Decision Outcome
Implement the Process Wait and Exit Synchronization Engine (`vessel_process_wait`):

1. **Atomic Exit Coordination Channel (`internal/sandbox/process.go`)**:
   - `ManagedProcess` adds an exit channel `done chan struct{}` guarded by `doneOnce sync.Once`.
   - When the container process terminates (or is killed via SIGTERM/SIGKILL), `done` is closed atomically.
   - `SetExitCode(code int)` updates the exit code and unblocks any waiting listeners.

2. **Blocking Wait with Timeout (`WaitOutput`)**:
   - `ProcessManager.WaitOutput(ctx, id, timeout, maxBytes)` checks active running state:
     - If already exited: immediately drains remaining buffered output and returns final exit code.
     - If actively running: blocks on `select` between `proc.done`, a configurable timeout timer, or context cancellation.
     - If timeout expires while still running: returns accumulated output with `running: true` and `exit_code: null` without terminating the process.
     - If process finishes within timeout: returns consolidated output with `running: false` and the integer `exit_code`.

3. **Cumulative Budget Metering Integration**:
   - Time elapsed during `WaitProcess` is metered against the session's cumulative duration limit (`--max-duration`).
   - Drained output tokens are scrubbed, DLP-redacted, and metered against the cumulative token limit (`--max-tokens`).

4. **23rd Core MCP Tool (`vessel_process_wait`)**:
   - Parameters:
     - `process_id` (string, required): Process ID returned by `vessel_start_process`.
     - `timeout_seconds` (integer, optional): Maximum seconds to wait (default: 30, capped at 300).
     - `max_bytes` (integer, optional): Maximum bytes to drain from buffer.
   - Returns structured JSON payload:
     ```json
     {
       "process_id": "proc-1731742400",
       "command": "npm test",
       "running": false,
       "exit_code": 0,
       "output": "PASS 14 tests\nDone in 3.42s.",
       "tokens_estimate": 45
     }
     ```

## Positive Consequences
- **Elimination of Agent Polling Loops**: Agents can start a build and wait for its completion in a single roundtrip.
- **Drastic Token Savings**: Replaces multiple polling cycles with a single structured response.
- **Timeout Safety**: Built-in timeout ceiling (max 300s) prevents runaway hangs while returning partial progress.

## Invariants Maintained
- **Token-Hygiene & Zero-Leak Invariant**: Drained output passes through the token scrubber and secret vault.
- **Resource-Bounding Invariant**: Cumulative execution time and token consumption remain bound by budget circuit breakers.

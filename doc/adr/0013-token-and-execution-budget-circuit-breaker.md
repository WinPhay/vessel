# ADR 0013: Cumulative Token and Execution Budget Circuit Breaker

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
Autonomous coding agents operating in unconstrained loops (via Model Context Protocol or CLI runners) present serious operational and financial hazards:
1. **Runaway Loops & Context Bloat**: An agent stuck in a compile-fail-retry loop or recursive file search can generate millions of tokens in minutes, wasting compute and context window limits.
2. **Infinite Command Hangs**: Long-running background compilation, web downloads, or infinite loops can consume workstation CPU cores indefinitely.
3. **Missing Circuit Breakers**: While Vessel previously supported per-command timeouts and resource cgroups (`--memory`, `--cpus`), it had no cumulative session-level metering or circuit breaker to halt a rogue agent before catastrophic token exhaustion.

## Decision Outcome
Implement the Cumulative Token and Execution Budget Circuit Breaker (`internal/sandbox/budget.go`):

1. **Budget Metering Engine (`internal/sandbox/budget.go`)**:
   - `BudgetMonitor`: Thread-safe monitor aggregating cumulative token output across all commands (`vessel_exec`), background process reads (`vessel_process_output`), and Git diff inspections (`vessel_worktree_diff`).
   - Accumulates cumulative wall-clock execution duration across sandbox invocations.
   - Enforces configurable thresholds:
     - `MaxTokens`: Maximum cumulative tokens before tripping (0 = unlimited).
     - `MaxDuration`: Maximum cumulative execution time before tripping (0 = unlimited).
   - Once a limit is breached, the monitor transitions to **Tripped** state, failing subsequent execution attempts with `ErrCircuitBreakerTripped`.

2. **Integration into Sandbox Runtimes (`internal/sandbox/docker.go`)**:
   - `DockerSandboxConfig` adds `MaxTokens int64` and `MaxDuration time.Duration`.
   - Every `Exec`, `ReadProcessOutput`, and `WorktreeDiff` checks the circuit breaker state prior to execution.
   - Output tokens and execution durations are automatically recorded post-execution into the session monitor.
   - Fail-closed invariant: Tripped sessions refuse further execution until explicitly reset.

3. **CLI & MCP Surface (`cmd/vessel/main.go` & `internal/mcp/server.go`)**:
   - CLI flags on `vessel exec` and `vessel mcp`:
     - `--max-tokens <N>`: Maximum cumulative tokens permitted before trip (e.g. `--max-tokens 50000`).
     - `--max-duration <duration>`: Maximum cumulative execution duration (e.g. `--max-duration 15m`).
   - `vessel_status` in MCP returns live `budget` telemetry:
     ```json
     {
       "budget": {
         "cumulative_tokens": 12450,
         "max_tokens": 50000,
         "cumulative_duration_ms": 3200,
         "max_duration_ms": 900000,
         "tripped": false
       }
     }
     ```

## Positive Consequences
- **Financial & Token Safety**: Operators can enforce hard caps on agent runs, preventing runaway costs.
- **Fail-Closed Protection**: Rogue agents cannot continue hammering terminal tools once a budget limit is reached.
- **Full Observability**: Live budget metrics allow IDE clients (Claude Desktop, Cursor) to track cumulative consumption.

## Invariants Maintained
- **Host-Isolation Invariant**: Circuit breaker enforcement happens within Vessel before dispatching commands.
- **Zero-Leak Invariant**: Token calculations continue using sanitized, redacted outputs from the Shannon entropy vault.

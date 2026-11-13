# ADR 0007: Code Search Engine, Atomic Patching, and CLI Process Control

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-13

## Context & Problem Statement
With the addition of file operations (ADR-0003), browser tools (ADR-0004), and background process management (ADR-0006), AI coding agents in Vessel still face two key developer workflow friction points:
1. **Inefficient Code Search**: Finding functions, types, and strings across a multi-file workspace required agents to execute container shell commands (`vessel_exec grep -rn ...`), incurring container exec latency and risking unescaped bash argument injection.
2. **Brittle File Mutations**: Rewriting entire files via `vessel_write_file` consumes excessive context tokens and risks accidental regressions in large files (>300 lines). Agents need an atomic, line-targeted unified diff/patch tool (`vessel_apply_patch`) with dry-run safety and exact context verification.
3. **Host CLI Process Visibility**: While MCP clients could manage background processes, developers using the CLI directly had no standalone subcommands to inspect or kill running container processes without launching an MCP inspector.

## Decision Outcome
Extend Vessel with a native fast search engine, atomic patch tool, and CLI process commands:

1. **Workspace Regex Search (`vessel_grep`)**:
   - `pattern`: Regex or substring to match.
   - `path`: Target directory or file within workspace (default: ".").
   - `file_pattern`: Glob filter (e.g., `*.go`, `*.ts`).
   - `max_matches`: Match cap to guard LLM token context.
   - Enforces path-jail boundaries, skips `.git`, `.vessel`, and binary files.

2. **Atomic Unified Diff Patcher (`vessel_apply_patch`)**:
   - Parses standard unified diff format (`--- a/...`, `+++ b/...`, `@@ ... @@`).
   - Verifies target file exists within workspace jail.
   - Tests all context lines before applying mutations; fails atomically if any hunk fails to match.
   - Supports `dry_run: true` to validate patch applicability without mutating disk state.

3. **CLI Process Control**:
   - Adds `vessel process [list | kill <id> | logs <id>]` subcommands.
   - Formats process IDs, uptime, commands, PTY flags, and terminal logs for human developers.

## Positive Consequences
- **Low Latency & High Signal**: Fast in-process grep without spinning up container exec shells.
- **Context Preservation**: Unified diffs allow agents to perform surgical modifications with 90% fewer tokens than full-file writes.
- **Operator Observability**: Direct terminal inspection of background processes running in the sandbox.

## Invariants Maintained
- **Zero Jail Breakout**: Both search and patch engines resolve paths through `ResolveSecurePath`.
- **Atomic Reliability**: Patches either succeed 100% or leave the target file untouched.

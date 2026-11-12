# ADR 0003: Native Path-Jailed File Operations for MCP

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-12

## Context & Problem Statement
Prior to this decision, coding agents interfacing with Vessel had to execute shell commands (`cat`, `sed`, `echo`, `head`) via `vessel_exec` to inspect, edit, or explore files in `/workspace`.

This introduced several critical friction points:
1. **Shell Escaping Vulnerabilities**: Writing multi-line code files via bash heredocs or string concatenation frequently corrupted quotes, dollar signs, and backticks.
2. **Token Inefficiency**: Shell `cat` lacks structured pagination, making it easy to accidentally flood LLM context windows with large files.
3. **Subshell Overhead**: Spawning container subshells for simple file reads adds unnecessary latency (~50-100ms per file read).
4. **Boundary Verification**: Shell commands can attempt to read container paths outside `/workspace` (e.g., `/etc/shadow`, `/root`).

## Decision Outcome
Expand the Vessel MCP tool surface by adding three dedicated, path-jailed file operations:
1. `vessel_read_file`:
   - Parameters: `path` (string, required), `start_line` (int, optional, 1-indexed), `end_line` (int, optional).
   - Returns: text content, total lines, sliced line count, and token estimate.
2. `vessel_write_file`:
   - Parameters: `path` (string, required), `content` (string, required), `overwrite` (bool, optional, default: true).
   - Behavior: Creates parent directories automatically; writes via atomic `.tmp` file and rename to prevent partial writes.
3. `vessel_list_dir`:
   - Parameters: `path` (string, optional, default: "."), `recursive` (bool, optional, default: false), `max_depth` (int, optional, default: 2).
   - Behavior: Returns structured JSON list of entries (name, path, is_dir, size_bytes). Automatically skips `.vessel` and `.git`.

## Security & Jail Invariants
1. **Path-Jail Boundary**: All target paths are cleaned (`filepath.Clean`) and resolved strictly against the configured workspace directory. Any path resolving outside `/workspace` immediately aborts with `path escapes workspace sandbox`.
2. **Symlink Boundary**: Symlinks that point outside `/workspace` are rejected to prevent symlink traversal attacks.
3. **Atomic Replacement**: File writing is atomic; a crash or abort during writing leaves the original file intact.

## Positive Consequences
- **Direct & Deterministic**: Coding agents (Claude, Cursor, Cline) get fast, error-free file manipulation without shell quoting bugs.
- **Context Window Protection**: Line-based pagination prevents context overflow.
- **Backward Compatible**: Existing `vessel_exec`, `vessel_checkpoint`, and `vessel_rollback` tools remain unchanged.

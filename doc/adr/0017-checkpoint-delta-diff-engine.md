# ADR 0017: Checkpoint Delta Diff & Workspace Inspection Engine

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
In ADR-0001, Vessel established point-in-time snapshot and rollback capabilities (`vessel_checkpoint`, `vessel_rollback`), enabling agents to safely snapshot workspace state before speculative edits and restore prior states when tasks fail.

However, agents and human developers faced an observability blind spot:
1. **Blind Rollbacks & Destructive Inspection**: To inspect what changes occurred since a checkpoint, an agent had to either read individual files or rollback destructively to the prior state.
2. **Non-Git Workspace Gap**: While `vessel_worktree_diff` provides Git diffs for ephemeral worktrees, standalone directories, scratchpads, or non-git projects lacked any unified diff or change inspection mechanism.
3. **Structured Change Summaries**: LLM agents require token-efficient change metadata (files modified, added, deleted, addition/deletion counts, and estimated tokens) before deciding whether to commit, rollback, or refine edits.

## Decision Outcome
Implement the Checkpoint Delta Diff & Workspace Inspection Engine (`vessel_checkpoint_diff` and `vessel checkpoint diff`):

1. **Pure Go Unified Diff & Delta Engine (`internal/sandbox/snapshot.go`)**:
   - `SnapshotManager.DiffSnapshot(tagA, tagB string, maxBytes int) (*DiffResult, error)` compares two checkpoints or a checkpoint against the live workspace (`live` or empty `tagB`).
   - Implements a zero-dependency Myers/LCS edit matrix generating standard unified diffs (`--- a/path`, `+++ b/path`, `@@ -l,s +l,s @@`).
   - Recursively traverses checkpoint directories and live workspace, identifying file states: `modified`, `added`, and `deleted`.
   - Ignores `.vessel` metadata and `.git` internal directories to preserve signal-to-noise ratio.
   - Enforces workspace jail boundaries on all compared paths.

2. **Diff Metrics & Token Estimation**:
   - Counts total `files_changed`, `additions`, and `deletions`.
   - Computes conservative `token_estimate` (approx 1 token per 4 characters) to assist agents in managing LLM context budgets.
   - Supports bounded output via `max_bytes` truncation to prevent context blowup on massive changesets.

3. **CLI Interface (`cmd/vessel/main.go`)**:
   - Subcommand `vessel checkpoint diff <tagA> [<tagB>] [--workspace <dir>]`:
     - Compares `<tagA>` against live workspace by default (`<tagB>` defaults to `"live"`).
     - Compares `<tagA>` against `<tagB>` when two tags are supplied.
     - Prints structured summary: files changed, additions, deletions, token estimate, file change list, and unified diff.

4. **24th Core MCP Tool (`vessel_checkpoint_diff`)**:
   - Registered in `internal/mcp/server.go`.
   - Parameters:
     - `tag_a` (string, required): Baseline checkpoint tag name.
     - `tag_b` (string, optional): Target checkpoint tag name, or `"live"` (default: `"live"`).
     - `max_bytes` (integer, optional): Maximum characters/bytes of diff output to return.
   - Returns structured JSON payload:
     ```json
     {
       "tag_a": "pre-refactor",
       "tag_b": "live",
       "files_changed": 2,
       "additions": 14,
       "deletions": 3,
       "token_estimate": 85,
       "files": [
         {"path": "pkg/auth/login.go", "status": "modified"},
         {"path": "pkg/auth/login_test.go", "status": "added"}
       ],
       "diff": "--- a/pkg/auth/login.go\n+++ b/pkg/auth/login.go\n..."
     }
     ```

## Positive Consequences
- **Non-Destructive Workspace Inspection**: Agents can inspect differences across checkpoints or against live state without rollbacks.
- **Zero External Dependencies**: Operates natively in pure Go without requiring `/usr/bin/diff`, Git, or external host binaries.
- **Context-Window Friendly**: Provides granular file lists and token estimation, enabling agents to selectively pull file diffs or inspect summaries.

## Invariants Maintained
- **Workspace Jail Invariant**: Snapshot comparison is strictly constrained within the workspace root; traversal attempts outside the jail are rejected.
- **Zero-Pollution Invariant**: Metadata under `.vessel` and Git trees under `.git` are ignored during diff comparisons.

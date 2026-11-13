# ADR 0010: Git Worktree Shield and Workspace Review Gate

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-13

## Context & Problem Statement
When autonomous coding agents operate inside Vessel's container jail, file modifications (e.g. via `vessel_write_file`, `vessel_apply_patch`, or shell commands in `vessel_exec`) are written directly to the host directory mounted at `/workspace`.

In active software projects, direct host workspace mounting introduces critical operational hazards:
1. **Host Unstaged Work Clobbering**: If a developer has unstaged modifications or half-written work in their repository, an agent rewriting files can accidentally overwrite developer edits, leading to unrecoverable data loss.
2. **Branch Pollution**: Agent edits occur directly on whichever Git branch happens to be checked out on the host, mixing untrusted agent modifications with developer commits.
3. **Missing Operator Review Gate**: Developers have no safe staging boundary to inspect a clean, isolated diff of *strictly what the agent produced* before deciding whether to accept, commit, or discard the output.

While instant filesystem snapshots (`vessel_checkpoint` / `vessel_rollback` in ADR-0002) protect against destruction, they require coarse-grained time-travel rollbacks rather than clean Git branch isolation.

## Decision Outcome
Introduce a Git Worktree Shield and Review Gate into Vessel:

1. **Ephemeral Git Worktree Isolation (`internal/sandbox/worktree.go`)**:
   - When `--worktree` is enabled, Vessel detects if `/workspace` is a Git repository.
   - If Git is present, Vessel creates an ephemeral worktree at `.vessel/worktrees/vessel-<session_id>` branched off `HEAD` (named `vessel/session-<session_id>`).
   - The container jail mounts this *isolated worktree* at `/workspace` instead of the host's primary working directory.
   - The developer's primary working tree remains 100% untouched and pristine during agent execution.

2. **Operator Review Gate & MCP Tools (19 -> 22 tools)**:
   - `vessel_worktree_status`: Returns current branch, tracking head, and list of untracked, modified, and staged files within the worktree.
   - `vessel_worktree_diff`: Returns the unified diff of all agent changes against the parent branch, with token estimation and line metrics (`files_changed`, `additions`, `deletions`).
   - `vessel_worktree_commit`: Atomically stages and commits the agent's work within the worktree branch with a specified commit message.

3. **CLI Subcommand & Flag Integration**:
   - Flag: `--worktree` on `vessel exec` and `vessel mcp` to automatically provision an isolated worktree for the execution session.
   - Subcommand: `vessel worktree [status | diff | commit <msg> | discard] [--workspace <dir>]`.

4. **Lifecycle & Cleanup**:
   - Worktree state can be committed or discarded (`git worktree remove --force`).
   - When a session finishes without `--keep-worktree`, ephemeral worktrees are cleanly removed, ensuring zero host directory bloat.

## Positive Consequences
- **Zero Host Pollution**: Developers can safely launch autonomous agents while continuing to work in their IDE on the same repository without file collision.
- **First-Class Git Integration**: Agent changes are naturally tracked on a Git branch, enabling standard PR and merge workflows.
- **Surgical Review**: Developers can review `vessel_worktree_diff` outputs before merging into `main`.

## Invariants Maintained
- **Host-Isolation Invariant**: Primary working tree is never touched while worktree shield is active.
- **Deterministic Token Scrubber**: Diffs returned to LLMs pass through token filtering and secret redaction.

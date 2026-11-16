# ADR 0018: Workspace Access Policy & Read-Only Path Rules

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
In ADR-0001, Vessel mounted the target host directory directly into `/workspace:rw` inside the container sandbox.

While this allows agents to write code and perform builds, it poses significant operational and security risks:
1. **Unbounded Write Surface**: By default, container processes and MCP write tools (`vessel_write_file`, `vessel_apply_patch`) have unrestricted write and delete authority over the entire workspace.
2. **Accidental Modification of Sensitive Assets**: Critical files such as `.env`, `.git/`, `.github/workflows/`, production database configs, or infrastructure scripts are vulnerable to hallucinations, destructive shell commands (`rm -rf *`), or corrupted edits.
3. **Auditing & Read-Only Use Cases**: Diagnostic, exploration, security auditing, and code review workflows require strict read-only execution guarantees under the Principle of Least Privilege.

## Decision Outcome
Implement the Workspace Access Policy & Read-Only Path Rules Engine (`PathPolicy`, `--read-only`, `--ro-path`, and `--deny-path`):

1. **Path Policy Engine (`internal/sandbox/policy.go`)**:
   - `PathPolicy` struct defines access constraints:
     - `ReadOnly bool`: Enforces complete read-only workspace access.
     - `ROPaths []string`: Paths or wildcard patterns protected against modifications.
     - `DenyPaths []string`: Paths or wildcard patterns completely masked/forbidden.
   - Normalized path matching supporting:
     - Exact path matches (e.g. `.env`).
     - Directory prefix inheritance (e.g. `.git/` protects all nested files).
     - Standard glob patterns (e.g. `*.pem`, `config/*.yaml`).
     - Recursive double-asterisk wildcards (e.g. `vendor/**`).
   - Granular policy checks:
     - `CheckRead(path)`: Returns `ErrPathDenied` if path is in `DenyPaths`.
     - `CheckWrite(path)`: Returns `ErrWorkspaceReadOnly`, `ErrPathDenied`, or `ErrPathReadOnly`.
     - `FilterEntries(entries)`: Automatically strips denied paths from directory listings.

2. **Dual-Layer Enforcement**:
   - **Docker Container Mount Layer (`internal/sandbox/docker.go`)**:
     - When `ReadOnly` is enabled: Mounts `/workspace` as `:ro` and attaches a 64MB `tmpfs` at `/tmp` (`rw,noexec,nosuid`) so compilers and scripts retain necessary scratch space.
     - For `ROPaths`: Layers `:ro` sub-bind mounts over host paths into container mount namespace.
     - For `DenyPaths`: Over-mounts `/dev/null` over denied files and empty `tmpfs` mounts over denied directories.
   - **MCP Tool Surface Layer (`internal/sandbox/file_ops.go`, `patch.go`, `search.go`, `internal/mcp/server.go`)**:
     - `vessel_read_file`: Validates read access; rejects denied paths with `ErrPathDenied`.
     - `vessel_write_file`: Validates write access; rejects attempts with `ErrWorkspaceReadOnly`, `ErrPathReadOnly`, or `ErrPathDenied`.
     - `vessel_apply_patch`: Validates patch target paths against `CheckWrite`.
     - `vessel_list_dir`: Filters out denied files from directory listings.
     - `vessel_grep`: Skips reading or walking through denied files and directories.
     - `vessel_status`: Reports active policy status (`read_only`, count of `ro_paths` and `deny_paths`).

3. **CLI Flags & Configuration (`cmd/vessel/main.go`)**:
   - Added to `vessel exec` and `vessel mcp`:
     - `--read-only`, `--ro`: Mount workspace in read-only mode.
     - `--ro-path <path>`: Repeatable flag for read-only protected paths/patterns.
     - `--deny-path <path>`: Repeatable flag for forbidden/masked paths.

## Positive Consequences
- **Least Privilege Guarantee**: AI agents can run in verified read-only audit mode without fear of accidental file modifications.
- **Critical Asset Protection**: Developers can safeguard `.env`, `.git/`, and CI workflows from speculative agent modifications.
- **Consistent Dual-Layer Defense**: Security rules are enforced at both the container kernel mount layer and the application MCP layer.

## Invariants Maintained
- **Workspace Access Policy Invariant**: When `--read-only` or fine-grained path rules are configured, they are strictly enforced across container mounts and MCP tool calls.
- **Zero-Pollution & Jail Invariant**: Workspace containment checks remain strictly bound to the workspace root.

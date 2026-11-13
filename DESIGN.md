# DESIGN.md — vessel Architecture & Invariants

> Architectural invariants and structural principles for Vessel. Read before structural changes.

---

## 1. System Mission
Vessel provides a zero-leak, zero-destruction local execution jail for AI coding agents. It wraps untrusted bash/terminal execution in an ephemeral container with sub-second filesystem/memory rollbacks and token-filtered outputs.

---

## 2. Architecture Layers

```text
Host Environment (Developer Workstation)
 ├─ Agent Client (Claude Desktop / Cursor / Cline)
 └─ vessel (Go Static Binary)
     ├─ MCP stdio Server (Protocol translation & tool dispatch)
     ├─ Token Scrubber (Strips ANSI escapes, summarizes stderr/stdout, formats JSON)
     └─ Sandbox Controller (Docker SDK / MicroVM KVM Driver)
         │
         └─> Container Execution Jail (/workspace mount)
             ├─ Isolated Alpine/Ubuntu RootFS
             ├─ Stripped Non-Interactive Shell (PAGER=cat, DEBIAN_FRONTEND=noninteractive)
             └─ Pre-Baked Cognitive Tooling (ripgrep, git, curl)
```

---

## 3. Core Invariants (Non-Negotiable)

1. **Host-Isolation Invariant**: Untrusted commands executed via Vessel MUST NEVER run on the host OS directly. Commands are strictly dispatched inside the sandbox container.
2. **Mount Jail Invariant**: Only the targeted working directory is mounted into the container at `/workspace`. Host root, `~/.ssh`, `~/.bashrc`, and cloud credentials must never be mounted.
3. **Atomic Time-Travel Invariant**: Checkpointing a sandbox state produces an immutable snapshot tag. Calling rollback restores the exact filesystem snapshot in <1s.
4. **Token-Hygiene & Zero-Leak Invariant**: All raw command stdout/stderr, process output, grep results, and file reads must pass through the token scrubber and secret vault before returning to the MCP client. ANSI codes, terminal escapes, and sensitive credentials (API keys, private keys, database URLs, high-entropy tokens, registered secrets) are stripped and redacted to guarantee zero secret egress.
5. **Resource-Bounding Invariant**: Containers strictly enforce configurable kernel cgroup limits (`MemoryBytes`, `NanoCPUs`) to prevent infinite recursion, memory leaks, or CPU starvation on the host system.
6. **Git Worktree Shield Invariant**: When `--worktree` is enabled, the agent executes within an ephemeral Git worktree located at `.vessel/worktrees/vessel-<session>` on an isolated session branch. The developer's primary working tree remains 100% clean and untouched. Modifications can be reviewed via `vessel_worktree_diff` and committed via `vessel_worktree_commit` or discarded automatically on session termination.

---

## 4. MCP Tool Surface Contract

Vessel exposes 22 core tools over the Model Context Protocol:

| Tool Name | Parameters | Description |
| :--- | :--- | :--- |
| `vessel_exec` | `command: string`, `timeout_seconds?: int` | Executes command inside container; returns exit code, stdout, stderr, and token estimate. |
| `vessel_checkpoint` | `tag: string` | Creates a named checkpoint of container rootfs and workspace directory state. |
| `vessel_rollback` | `tag: string` | Restores container rootfs and workspace directory to the specified checkpoint state. |
| `vessel_status` | *(none)* | Returns container health, uptime, workspace path, and active checkpoint list. |
| `vessel_read_file` | `path: string`, `start_line?: int`, `end_line?: int` | Reads workspace file with optional line pagination, token count, and jail boundary verification. |
| `vessel_write_file` | `path: string`, `content: string`, `overwrite?: bool` | Atomically writes workspace file with auto-created parent directories and jail boundary verification. |
| `vessel_list_dir` | `path?: string`, `recursive?: bool`, `max_depth?: int` | Lists workspace directory entries, skipping `.vessel` and `.git` ignores. |
| `vessel_browse_url` | `url: string`, `wait_seconds?: int`, `include_links?: bool` | Visits URL in headless browser sandbox, rendering dynamic DOM and returning sanitized Markdown text. |
| `vessel_screenshot` | `url: string`, `output_path?: string`, `full_page?: bool`, `width?: int`, `height?: int` | Captures PNG screenshot of web page into workspace sandbox. |
| `vessel_extract_page` | `url: string` | Extracts structured interactive affordances (headings, links, inputs, buttons) as JSON. |
| `vessel_start_process` | `command: string`, `pty?: bool`, `rows?: int`, `cols?: int` | Spawns background or interactive process inside container jail; returns unique `process_id`. |
| `vessel_process_output` | `process_id: string`, `max_bytes?: int` | Reads unread stdout/stderr from process ring buffer with token estimation. |
| `vessel_process_write` | `process_id: string`, `input: string` | Writes standard input (keystrokes, commands, confirmation) into active process stdin. |
| `vessel_kill_process` | `process_id: string`, `signal?: string` | Sends termination signal (SIGTERM/SIGKILL) to stop a background process. |
| `vessel_grep` | `pattern: string`, `path?: string`, `file_pattern?: string`, `max_matches?: int`, `case_sensitive?: bool` | Fast regex and substring search across workspace files with token estimates. |
| `vessel_apply_patch` | `patch: string`, `dry_run?: bool` | Applies a unified diff patch atomically to a target file in the workspace sandbox. |
| `vessel_spawn_service` | `name: string`, `image: string`, `env?: object`, `command?: string` | Spawns an isolated sidecar container on the session network; reachable via internal DNS `name:port`. |
| `vessel_stop_service` | `name: string` | Stops and removes an active sidecar service from the session mesh. |
| `vessel_list_services` | *(none)* | Returns status, image, uptime, and hostnames for all active sidecars. |
| `vessel_worktree_status` | *(none)* | Returns Git worktree status, current branch, base branch, modified and untracked file lists. |
| `vessel_worktree_diff` | `max_bytes?: int` | Returns unified diff against HEAD within the isolated worktree, token estimate, and file change metrics. |
| `vessel_worktree_commit` | `message: string`, `author_name?: string`, `author_email?: string` | Stages all changes and commits them within the session worktree branch with custom or default author metadata. |

---

## 5. Non-Goals

- **Not a Cloud SaaS Platform**: Vessel is local-first. It does not handle multi-tenant web billing, public user logins, or remote cloud hosting.
- **Not an LLM Prompt Wrapper**: Vessel does not call LLM APIs or prompt models. It is an execution substrate consumed by external agent engines.
- **Not a Full Desktop GUI**: Vessel is a CLI and MCP daemon.

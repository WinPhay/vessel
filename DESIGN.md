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
4. **Token-Hygiene Invariant**: All raw command stdout/stderr must pass through the token scrubber before returning to the MCP client. ANSI codes, binary garbage, and terminal escapes are stripped to protect LLM context windows.
5. **Resource-Bounding Invariant**: Containers strictly enforce configurable kernel cgroup limits (`MemoryBytes`, `NanoCPUs`) to prevent infinite recursion, memory leaks, or CPU starvation on the host system.

---

## 4. MCP Tool Surface Contract

Vessel exposes 7 core tools over the Model Context Protocol:

| Tool Name | Parameters | Description |
| :--- | :--- | :--- |
| `vessel_exec` | `command: string`, `timeout_seconds?: int` | Executes command inside container; returns exit code, stdout, stderr, and token estimate. |
| `vessel_checkpoint` | `tag: string` | Creates a named checkpoint of container rootfs and workspace directory state. |
| `vessel_rollback` | `tag: string` | Restores container rootfs and workspace directory to the specified checkpoint state. |
| `vessel_status` | *(none)* | Returns container health, uptime, workspace path, and active checkpoint list. |
| `vessel_read_file` | `path: string`, `start_line?: int`, `end_line?: int` | Reads workspace file with optional line pagination, token count, and jail boundary verification. |
| `vessel_write_file` | `path: string`, `content: string`, `overwrite?: bool` | Atomically writes workspace file with auto-created parent directories and jail boundary verification. |
| `vessel_list_dir` | `path?: string`, `recursive?: bool`, `max_depth?: int` | Lists workspace directory entries, skipping `.vessel` and `.git` ignores. |

---

## 5. Non-Goals

- **Not a Cloud SaaS Platform**: Vessel is local-first. It does not handle multi-tenant web billing, public user logins, or remote cloud hosting.
- **Not an LLM Prompt Wrapper**: Vessel does not call LLM APIs or prompt models. It is an execution substrate consumed by external agent engines.
- **Not a Full Desktop GUI**: Vessel is a CLI and MCP daemon.

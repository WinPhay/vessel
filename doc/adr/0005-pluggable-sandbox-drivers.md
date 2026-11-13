# ADR 0005: Pluggable Sandbox Drivers and gVisor/runsc Runtime Isolation

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-13

## Context & Problem Statement
Currently, Vessel defaults to container execution using the host Docker daemon's default OCI runtime (typically `runc`).
While `runc` isolates user-space processes via Linux namespaces and cgroups, containers still share the host Linux kernel. When running autonomous AI agents executing untrusted code or arbitrary third-party scripts, kernel vulnerabilities or zero-day exploits could facilitate container breakout into the developer's host system.

Furthermore, as Vessel progresses towards Phase 2 (MicroVM / KVM / Firecracker execution), the sandbox engine requires:
1. Hardened application-kernel sandboxing (e.g. Google's gVisor `runsc` OCI runtime, which intercepts and handles Linux syscalls in user space).
2. Pluggable driver selection (`docker`, `mock`, and `microvm` scaffold).
3. Runtime configuration parameters passed transparently via CLI (`--runtime`) and MCP server configurations.

## Decision Outcome
Introduce a pluggable driver architecture and runtime selection into Vessel:

1. **Pluggable Driver Abstraction (`internal/sandbox/driver.go`)**:
   - `type DriverType string` (`DriverDocker`, `DriverMock`, `DriverMicroVM`).
   - `type DriverCapabilities` struct detailing capabilities: `SupportsMemoryRollback`, `SupportsFilesystemRollback`, `SupportsAirgapNetwork`, `SupportsResourceQuotas`, `KernelIsolated`.
   - Driver registry and metadata inspection (`GetDriverCapabilities`, `NormalizeDriverType`).
   - MicroVM driver scaffolding (`MicroVMSandbox`) contract defining the interface and lifecycle requirements for future hypervisor sockets.

2. **OCI Runtime Selection on Docker Driver**:
   - Add `Runtime string` to `DockerSandboxConfig`.
   - Support standard runtimes: `runc` (default), `runsc` (Google gVisor sandbox), `kata` (Kata Containers).
   - Pass `hostCfg.Runtime = cfg.Runtime` to `ContainerCreate` when specified.

3. **CLI & MCP Server Exposure**:
   - Add `--runtime` flag to `vessel exec` and `vessel mcp` commands.
   - Update `vessel status` to report active OCI runtime and available drivers.

## Positive Consequences
- **Adversarial Hardening**: Agents executing arbitrary untrusted code can run under gVisor (`--runtime runsc`), preventing kernel-level escapes.
- **Architectural Extensibility**: Clean foundation for Phase 2 MicroVM (Firecracker/Cloud-Hypervisor) without refactoring the MCP layer.
- **Backwards Compatibility**: Default behavior remains unchanged (`runc` / host daemon default).

## Invariants Maintained
- Zero host command execution outside container/jail.
- Verification gates must pass with 0 errors across all drivers.

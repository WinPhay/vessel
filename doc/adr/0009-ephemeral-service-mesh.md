# ADR 0009: Ephemeral Service Mesh and Isolated Sidecars

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-13

## Context & Problem Statement
Autonomous AI coding agents operating on real-world web applications and microservices frequently execute tests and build scripts (`npm test`, `pytest`, `cargo test`) that require backing services:
- Relational databases (e.g., PostgreSQL, MySQL, MariaDB)
- In-memory caches and key-value stores (e.g., Redis, KeyDB, Memcached)
- Document/vector stores (e.g., MongoDB, Elasticsearch)
- Mock HTTP APIs and webhooks (e.g., WireMock, Prism)

Historically in Vessel, running these backing dependencies posed fundamental architectural barriers:
1. **Docker-in-Docker Prohibited**: The sandbox execution container deliberately lacks access to the host `/var/run/docker.sock` to prevent container escapes. The agent cannot run `docker run -d postgres` inside the jail.
2. **Host Database Pollution**: Running backing databases on the developer's host machine risks port conflicts (e.g., host already running Postgres on `:5432`), pollutes the host environment, and risks destructive agent queries (`DROP DATABASE`) impacting real local development data.
3. **Port Exfiltration Hazard**: Exposing container ports directly to `0.0.0.0` or `127.0.0.1` on the host violates the Host-Isolation Invariant.

## Decision Outcome
Introduce an Ephemeral Service Mesh architecture into Vessel that allows agents and operators to run isolated sidecar containers alongside `/workspace`:

1. **Session-Scoped Internal Network (`internal/sandbox/service.go`)**:
   - Vessel dynamically provisions an isolated, session-scoped Docker bridge network (`vessel-net-<session_id>`).
   - The primary `/workspace` container and all spawned sidecars attach to this shared bridge network.
   - Services communicate via Docker internal DNS using simple service aliases (e.g., `postgres:5432`, `redis:6379`, `mock-api:8080`).

2. **Host-Isolation & Port Invariants**:
   - **Zero Host Port Binding**: Sidecar containers NEVER bind ports to the host interface (`HostConfig.PortBindings` is nil). They are reachable ONLY from within the internal bridge network.
   - **Restricted Privileges**: Sidecars run with dropped Linux capabilities (`CapDrop: ALL`) and `no-new-privileges: true`.
   - **Resource Bounded**: Sidecars enforce memory and CPU cgroups quotas to prevent denial-of-service on the host machine.
   - **No Host Volume Mounts**: Sidecars run with ephemeral root filesystems and disposable Docker volumes; they are prohibited from mounting host filesystem paths.

3. **Lifecycle Coupling**:
   - Sidecar containers are registered in a thread-safe `ServiceManager`.
   - When `Sandbox.Close()` is invoked (or the CLI process terminates via SIGINT/SIGTERM), all sidecar containers are automatically stopped and removed, and the session bridge network is deleted.

4. **MCP Tool Expansion (16 -> 19 tools)**:
   - `vessel_spawn_service`: Spawns a sidecar container given `name: string`, `image: string`, `env?: map[string]string`, `command?: string`.
   - `vessel_stop_service`: Terminates and removes a running sidecar by `name: string`.
   - `vessel_list_services`: Returns structured status, uptime, image, and network aliases for all active sidecars.

5. **CLI Subcommand & Flag Integration**:
   - CLI command: `vessel service [list | spawn <name> <image> | stop <name>] [--workspace <dir>]`.
   - Repeatable CLI flag: `--service <name>=<image>` on `vessel exec` and `vessel mcp` to auto-provision required dependencies prior to executing agent commands.

## Positive Consequences
- **Realistic Integration Testing**: Agents can run full-stack tests against real database engines without mock limitations.
- **Zero Host Impact**: No host ports consumed, no host database corruption, zero residual containers left after agent termination.
- **DLP Synergy**: Passwords passed to services are automatically registered into `tokenfilter.Vault` (ADR-0008), ensuring credentials never leak in stdout/stderr.

## Invariants Maintained
- **Host-Isolation Invariant**: Sidecars expose zero host ports; communications remain strictly inside the internal Docker bridge network.
- **Zero Residual Pollution**: 100% automated cleanup of all sidecars and network namespaces on shutdown.

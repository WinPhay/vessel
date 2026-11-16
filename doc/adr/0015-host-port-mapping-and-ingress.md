# ADR 0015: Host Port Mapping and Local Ingress Proxy for Sidecar Services

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
In ADR-0009, Vessel introduced an ephemeral service mesh allowing autonomous agents to spin up sidecar services (e.g., PostgreSQL, Redis, MongoDB, mock API servers) on an internal session bridge network (`vessel-net-<session>`). To guarantee zero host interference, ADR-0009 strictly set `PortBindings` to `nil` (Zero Host Port Exposure).

However, real-world development workflows frequently require operator inspection and host connectivity:
1. **Host Database Tooling**: GUI database clients (DBeaver, TablePlus, DataGrip) and local CLI utilities (`psql`, `redis-cli`) running on the developer workstation cannot connect to sidecars isolated entirely within a Docker bridge.
2. **Local Browser Verification**: When an agent spins up an HTTP API or web frontend service, developers cannot preview or interact with the running web application in their host browser.
3. **Missing Local Ingress**: While service-to-service communication functioned seamlessly within the container mesh, host-to-service ingress was completely absent.

## Decision Outcome
Implement configurable, loopback-locked host port forwarding for sidecar services:

1. **Security Invariant — Mandatory Loopback Isolation (`127.0.0.1`)**:
   - Host port bindings MUST strictly bind to `127.0.0.1` (localhost only).
   - Any attempt to bind to `0.0.0.0`, wildcard interfaces, or external host IPs is rejected fail-closed with:
     `insecure port binding in "<spec>": host IP must be 127.0.0.1 (public host exposure forbidden)`
   - This preserves the Host-Isolation Invariant: database credentials or development servers running in Vessel can never be exposed to the local network (LAN) or public internet.

2. **Port Specification Parsing (`internal/sandbox/service.go`)**:
   - `ParsePortSpecs(specs []string)` validates syntax and bounds:
     - `5432:5432` → `127.0.0.1:5432 -> 5432/tcp`
     - `127.0.0.1:8080:80` → `127.0.0.1:8080 -> 80/tcp`
     - `localhost:3000:3000` → `127.0.0.1:3000 -> 3000/tcp`
     - `6379` → `127.0.0.1 (ephemeral) -> 6379/tcp`
     - `9000:9000/udp` → `127.0.0.1:9000 -> 9000/udp`
   - Validates that port numbers fall within the valid TCP/UDP range `1–65535`.

3. **Runtime Engine Integration**:
   - `SpawnService(ctx, name, image, env, cmd, ports ...string)` uses a variadic `ports` parameter to maintain 100% backwards compatibility with all existing callers.
   - Populates Docker `HostConfig.PortBindings` and `Config.ExposedPorts`.
   - Post-startup inspection retrieves dynamically assigned host ports when ephemeral ports are requested.
   - `ServiceInfo.Ports` exposes human-readable active port descriptions (e.g. `["127.0.0.1:5432->5432/tcp"]`).

4. **MCP Interface (`internal/mcp/server.go`)**:
   - `vessel_spawn_service` schema updated with optional `ports: string[]` parameter.
   - `vessel_list_services` includes active `ports` array in service metadata.

5. **CLI Surface (`cmd/vessel/main.go`)**:
   - Added `--service-port` and `--port` flags to `vessel exec` and `vessel mcp`.
   - Supports named service targeting (e.g., `--port db=5432:5432`) or shorthand default targeting (`--port 5432:5432`).

## Positive Consequences
- **Developer Ergonomics**: Operators can connect host clients (DBeaver, Postman, web browsers) directly to sandbox sidecars.
- **Fail-Closed Security**: Absolute guarantee against accidental public/LAN exposure via strict `127.0.0.1` enforcement.
- **Contract Stability**: Zero breaking changes to existing MCP tools, CLI parameters, or Go package APIs.

## Invariants Maintained
- **Host-Isolation Invariant**: Only explicitly declared ports are bound, and all bindings are strictly restricted to `127.0.0.1`.
- **Zero-Leak Invariant**: Dynamic secrets injected into sidecars (`POSTGRES_PASSWORD`, tokens) remain protected by the token scrubber and DLP vault.

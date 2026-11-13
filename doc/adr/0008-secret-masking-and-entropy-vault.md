# ADR 0008: Secret Masking and Dynamic Entropy Vault

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-13

## Context & Problem Statement
Vessel's foundational mission is to provide a "zero-leak, zero-destruction local execution jail for AI coding agents."
While filesystem destruction has been eliminated via instant copy-on-write snapshots (ADR-0002) and kernel escape has been mitigated via pluggable drivers and gVisor isolation (ADR-0005), autonomous agents executing inside `/workspace` remain vulnerable to **credential exfiltration and context leakage**:

1. **Unscrubbed Secret Egress**: When an agent inspects `.env` files, runs `printenv`, or triggers crash stack traces and git logs, plaintext API tokens (OpenAI, Anthropic, AWS, GitHub, GCP, Slack), private cryptographic keys (RSA, OpenSSH), database URLs, and JWTs pass unredacted into stdout/stderr. These outputs are transmitted over stdio MCP to external LLM provider APIs, exposing sensitive developer credentials to third-party model logs.
2. **Missing Dynamic Secret Injection**: Developers frequently need agents to execute scripts against real APIs or staging databases. Currently, passing environment variables into the container risks immediate echoing and exfiltration by the LLM.
3. **Bespoke / Unprefixed Secrets**: Beyond standard token prefixes (e.g., `sk-`, `ghp_`), custom API keys and base64/hex authentication strings escape simple keyword filters.

## Decision Outcome
Introduce a multi-layered Secret Masking & Dynamic Entropy Vault into Vessel's core token filtering engine:

1. **Pattern-Based Secret Redactor (`internal/tokenfilter/redactor.go`)**:
   - High-performance regex catalog matching sensitive credential signatures:
     - Cloud & LLM API keys: OpenAI (`sk-...`, `sk-proj-...`), Anthropic (`sk-ant-...`), AWS (`AKIA...`), GitHub PATs (`ghp_...`, `gho_...`, `ghu_...`, `ghs_...`, `ghr_...`), Google/GCP (`AIza...`), Slack tokens (`xoxb-...`, `xoxp-...`), HuggingFace (`hf_...`).
     - Cryptographic keys: `-----BEGIN (RSA|EC|OPENSSH|DSA|PGP) PRIVATE KEY-----`.
     - Database URLs: Connection URIs for PostgreSQL, MySQL, MongoDB, Redis, redacting URI passwords (`user:[REDACTED_PASSWORD]@host`).
     - Bearer tokens & JWTs (`Bearer <token>`, `eyJ...`).
   - Replaces matches with semantic redaction badges: `[REDACTED:<PATTERN_NAME>]`.

2. **Synthetic Dynamic Vault (`Vault`)**:
   - A thread-safe in-memory vault storing registered key-value secrets.
   - Any secret injected via CLI (`--secret KEY=VALUE`) is passed into container environment variables while registering `VALUE` in the vault.
   - Any occurrence of `VALUE` anywhere in output streams is scrubbed to `[REDACTED:KEY]`.

3. **Shannon Entropy Anomaly Scanner**:
   - Evaluates Shannon entropy on candidate tokens of length $\ge 32$ without known prefixes:
     $$H = -\sum_{i=1}^{n} p(x_i) \log_2 p(x_i)$$
   - Redacts tokens exceeding entropy thresholds ($H \ge 4.5$ for alphanumeric/base64; $H \ge 3.8$ for hex) to catch arbitrary high-entropy keys, while preserving UUIDs, file paths, and git commit hashes.
   - Replaces with `[REDACTED:HIGH_ENTROPY_SECRET]`.

4. **Universal Egress Integration**:
   - Integrates directly into `tokenfilter.CleanOutput` and provides `RedactOutput(text string) string` and `RedactOutputWithVault(text string, vault *Vault) string`.
   - Protects:
     - Container stdout & stderr (`vessel exec`, `vessel_exec`).
     - Background process output & ring buffers (`vessel_start_process`, `vessel_process_output`, `vessel process logs`).
     - Workspace code search matches (`vessel_grep`).
     - Workspace file reads (`vessel_read_file`).

5. **CLI & MCP Server Exposure**:
   - Adds repeatable `--secret <KEY=VALUE>` flag to `vessel exec` and `vessel mcp`.
   - Adds `--mask-secrets` boolean flag (default: `true`).

## Positive Consequences
- **Zero-Leak Guarantee**: Eliminates credential leakage to external LLM providers and local logs.
- **Safe Credential Injection**: Allows agents to run authenticated tasks (e.g. testing third-party APIs) without the LLM ever observing the underlying secret.
- **Defense in Depth**: Three complementary tiers (pattern catalog + dynamic registered vault + Shannon entropy scanner).
- **Sub-Millisecond Overhead**: Minimal processing latency added to token filtration pipeline.

## Invariants Maintained
- **Zero-Leak Invariant**: No registered secret or standard credential pattern shall emerge unredacted in MCP responses.
- **Deterministic Token Scrubber**: Token estimation accurately counts the post-redacted text to avoid context drift.

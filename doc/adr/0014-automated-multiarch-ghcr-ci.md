# ADR 0014: Automated Multi-Arch GHCR Agent Image CI Pipeline

- **Status**: Accepted
- **Deciders**: WinPhay
- **Date**: 2026-11-16

## Context & Problem Statement
Vessel provides specialized AgentOS container environments (`alpine`, `ubuntu`, `browser`) defined under `configs/`:
1. **Local Compilation Overhead**: Developers and autonomous agents on fresh workstations previously had to build container images locally using `scripts/build-images.sh`, incurring build delays and bandwidth costs.
2. **Cross-Architecture Discrepancies**: Modern development environments span `linux/amd64` (x86_64 servers/workstations) and `linux/arm64` (Apple Silicon M-series, AWS Graviton). Locally built images often default to single-architecture binaries, complicating cross-architecture deployments.
3. **Automated Validation Gap**: Modifying Dockerfiles carried the risk of undetected dependency breakage or repository package obsolescence without automated CI validation across both target CPU architectures.

## Decision Outcome
Implement the Automated Multi-Architecture GitHub Container Registry (GHCR) Pipeline (`.github/workflows/docker.yml`):

1. **Matrix Build Architecture (`.github/workflows/docker.yml`)**:
   - Automated matrix targeting all three AgentOS flavors:
     - `alpine`: Lightweight base (`configs/Dockerfile.alpine-agent`) — default Vessel image.
     - `ubuntu`: Full developer toolchain (`configs/Dockerfile.ubuntu-agent`).
     - `browser`: Headless Chromium agent (`configs/Dockerfile.browser-agent`).
   - Multi-arch platform compilation for `linux/amd64` and `linux/arm64` via `docker/setup-qemu-action@v3` and `docker/setup-buildx-action@v3`.
   - GitHub Actions Cache (`type=gha`, `mode=max`) for accelerated incremental layer caching.

2. **Tagging Taxonomy & Registry Target**:
   - Target registry: GitHub Container Registry (`ghcr.io/<owner>/vessel-agent`).
   - Normalizes owner and repository name to lowercase to comply with OCI/GHCR naming specifications.
   - Tag mapping:
     - Push to `main`: `ghcr.io/<owner>/vessel-agent:<flavor>`, plus `latest` alias for the default `alpine` image.
     - Release tags (`v*`): Semver tags `ghcr.io/<owner>/vessel-agent:<version>-<flavor>` and `ghcr.io/<owner>/vessel-agent:<major>.<minor>-<flavor>`.
     - Pull requests: Multi-arch build verification executed with `push: false`, validating buildability without publishing unverified artifacts.

3. **Least-Privilege Security Scoping**:
   - Scoped GitHub token permissions: `contents: read`, `packages: write`.
   - Uses native `${{ secrets.GITHUB_TOKEN }}` with zero long-lived external registry secrets or credentials.

4. **Local Tooling Parity (`scripts/build-images.sh`)**:
   - Updated CLI script with `--registry` flag and `REGISTRY` environment variable support.
   - Preserves offline local developer defaults (`vessel-agent:<flavor>`) while enabling custom registry tagging when specified.

## Positive Consequences
- **Instant Agent Spinup**: Workstations and CI runners can directly pull official images from GHCR without compiling images locally.
- **Universal Multi-Architecture Support**: Native support for both `x86_64` and `arm64` eliminates emulation overhead on Apple Silicon and Graviton hosts.
- **Automated Regression Prevention**: Dockerfile updates are continuously verified across all target architectures on pull requests.

## Invariants Maintained
- **Agent Environment Invariant**: Container images enforce non-interactive headless execution defaults (`TERM=dumb`, `DEBIAN_FRONTEND=noninteractive`, `PAGER=cat`, `GIT_PAGER=cat`).
- **Zero-Secret Invariant**: Images contain only public developer toolchains; zero credentials or tokens are baked into image layers.

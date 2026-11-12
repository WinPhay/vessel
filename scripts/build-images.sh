#!/usr/bin/env bash
set -euo pipefail

# Build standard Vessel AgentOS images
# Usage: ./scripts/build-images.sh [alpine|ubuntu|all]

TARGET="${1:-all}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

build_alpine() {
    echo "==> Building vessel-agent:alpine..."
    docker build \
        -t vessel-agent:alpine \
        -t vessel-agent:latest \
        -f "${ROOT_DIR}/configs/Dockerfile.alpine-agent" \
        "${ROOT_DIR}/configs"
    echo "==> Successfully built vessel-agent:alpine"
}

build_ubuntu() {
    echo "==> Building vessel-agent:ubuntu..."
    docker build \
        -t vessel-agent:ubuntu \
        -f "${ROOT_DIR}/configs/Dockerfile.ubuntu-agent" \
        "${ROOT_DIR}/configs"
    echo "==> Successfully built vessel-agent:ubuntu"
}

case "${TARGET}" in
    alpine)
        build_alpine
        ;;
    ubuntu)
        build_ubuntu
        ;;
    all)
        build_alpine
        build_ubuntu
        ;;
    *)
        echo "Usage: $0 [alpine|ubuntu|all]"
        exit 1
        ;;
esac

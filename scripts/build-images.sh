#!/usr/bin/env bash
set -euo pipefail

# Build standard Vessel AgentOS images
# Usage: ./scripts/build-images.sh [alpine|ubuntu|browser|all]

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

build_browser() {
    echo "==> Building vessel-agent:browser..."
    docker build \
        -t vessel-agent:browser \
        -f "${ROOT_DIR}/configs/Dockerfile.browser-agent" \
        "${ROOT_DIR}/configs"
    echo "==> Successfully built vessel-agent:browser"
}

case "${TARGET}" in
    alpine)
        build_alpine
        ;;
    ubuntu)
        build_ubuntu
        ;;
    browser)
        build_browser
        ;;
    all)
        build_alpine
        build_ubuntu
        build_browser
        ;;
    *)
        echo "Usage: $0 [alpine|ubuntu|browser|all]"
        exit 1
        ;;
esac

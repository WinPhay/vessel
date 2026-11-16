#!/usr/bin/env bash
set -euo pipefail

# Build standard Vessel AgentOS images
# Usage: ./scripts/build-images.sh [alpine|ubuntu|browser|all] [--registry <registry>]

TARGET="all"
REGISTRY="${REGISTRY:-}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        alpine|ubuntu|browser|all)
            TARGET="$1"
            shift
            ;;
        --registry|-r)
            REGISTRY="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 [alpine|ubuntu|browser|all] [--registry <registry>]"
            exit 1
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Helper function to invoke docker build with optional registry tag prefix
build_image() {
    local flavor="$1"
    local dockerfile="$2"
    local extra_tags=()

    echo "==> Building vessel-agent:${flavor}..."

    # Always tag locally as vessel-agent:<flavor>
    extra_tags+=(-t "vessel-agent:${flavor}")

    if [[ "${flavor}" == "alpine" ]]; then
        extra_tags+=(-t "vessel-agent:latest")
    fi

    # If registry prefix is specified, tag with registry prefix
    if [[ -n "${REGISTRY}" ]]; then
        local clean_registry="${REGISTRY%/}"
        extra_tags+=(-t "${clean_registry}/vessel-agent:${flavor}")
        if [[ "${flavor}" == "alpine" ]]; then
            extra_tags+=(-t "${clean_registry}/vessel-agent:latest")
        fi
    fi

    docker build \
        "${extra_tags[@]}" \
        -f "${ROOT_DIR}/configs/${dockerfile}" \
        "${ROOT_DIR}/configs"

    echo "==> Successfully built vessel-agent:${flavor}"
}

case "${TARGET}" in
    alpine)
        build_image "alpine" "Dockerfile.alpine-agent"
        ;;
    ubuntu)
        build_image "ubuntu" "Dockerfile.ubuntu-agent"
        ;;
    browser)
        build_image "browser" "Dockerfile.browser-agent"
        ;;
    all)
        build_image "alpine" "Dockerfile.alpine-agent"
        build_image "ubuntu" "Dockerfile.ubuntu-agent"
        build_image "browser" "Dockerfile.browser-agent"
        ;;
esac

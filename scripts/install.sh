#!/usr/bin/env bash
# ==============================================================================
# Vessel One-Line Installer
# Installs the latest verified Vessel static binary for your OS and architecture.
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/WinPhay/vessel/main/scripts/install.sh | bash
# Or with specific version:
#   curl -fsSL https://raw.githubusercontent.com/WinPhay/vessel/main/scripts/install.sh | bash -s -- v0.2.0
# ==============================================================================

set -euo pipefail

REPO="WinPhay/vessel"
TARGET_VERSION="${1:-}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m' # No Color

info() {
    printf "${BLUE}==>${NC} %b\n" "$1"
}

success() {
    printf "${GREEN}==>${NC} ${BOLD}%b${NC}\n" "$1"
}

warn() {
    printf "${YELLOW}Warning:${NC} %b\n" "$1"
}

error() {
    printf "${RED}Error:${NC} %b\n" "$1" >&2
    exit 1
}

# 1. Detect OS
OS_RAW="$(uname -s)"
case "${OS_RAW}" in
    Linux*)   OS="linux" ;;
    Darwin*)  OS="darwin" ;;
    MINGW*|MSYS*|CYGWIN*) OS="windows" ;;
    *) error "Unsupported operating system: ${OS_RAW}" ;;
esac

# 2. Detect Architecture
ARCH_RAW="$(uname -m)"
case "${ARCH_RAW}" in
    x86_64|amd64)   ARCH="amd64" ;;
    arm64|aarch64)  ARCH="arm64" ;;
    *) error "Unsupported architecture: ${ARCH_RAW}" ;;
esac

BINARY_NAME="vessel-${OS}-${ARCH}"
if [ "${OS}" = "windows" ]; then
    BINARY_NAME="${BINARY_NAME}.exe"
fi

# 3. Determine Target Release Version
if [ -z "${TARGET_VERSION}" ]; then
    info "Resolving latest release of Vessel..."
    # Query redirect header first to avoid GitHub API rate limits
    TARGET_VERSION="$(curl -sI "https://github.com/${REPO}/releases/latest" 2>/dev/null | grep -i "^location:" | sed -e 's/.*tag\///' | tr -d '\r\n')"
    if [ -z "${TARGET_VERSION}" ]; then
        # Fallback to GitHub API
        TARGET_VERSION="$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/' || true)"
    fi
    if [ -z "${TARGET_VERSION}" ]; then
        error "Could not determine latest release version. Specify version explicitly: bash install.sh v0.2.0"
    fi
fi

info "Installing ${BOLD}Vessel ${TARGET_VERSION}${NC} for ${BOLD}${OS}/${ARCH}${NC}..."

# 4. Prepare Temp Directory
TMP_DIR="$(mktemp -d -t vessel-install-XXXXXX)"
cleanup() {
    rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

BINARY_URL="https://github.com/${REPO}/releases/download/${TARGET_VERSION}/${BINARY_NAME}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${TARGET_VERSION}/checksums.txt"

info "Downloading ${BINARY_NAME}..."
if ! curl -sSL --fail "${BINARY_URL}" -o "${TMP_DIR}/${BINARY_NAME}"; then
    error "Failed to download binary from ${BINARY_URL}. Please check version tag."
fi

# 5. Checksum Verification
info "Verifying SHA-256 checksum..."
if curl -sSL --fail "${CHECKSUM_URL}" -o "${TMP_DIR}/checksums.txt"; then
    cd "${TMP_DIR}"
    EXPECTED_SHA="$(grep -F "${BINARY_NAME}" checksums.txt | awk '{print $1}' || true)"
    if [ -n "${EXPECTED_SHA}" ]; then
        if command -v sha256sum >/dev/null 2>&1; then
            ACTUAL_SHA="$(sha256sum "${BINARY_NAME}" | awk '{print $1}')"
        elif command -v shasum >/dev/null 2>&1; then
            ACTUAL_SHA="$(shasum -a 256 "${BINARY_NAME}" | awk '{print $1}')"
        else
            ACTUAL_SHA=""
            warn "No sha256sum or shasum tool found; skipping checksum verification."
        fi

        if [ -n "${ACTUAL_SHA}" ]; then
            if [ "${EXPECTED_SHA}" != "${ACTUAL_SHA}" ]; then
                error "Checksum mismatch! Expected: ${EXPECTED_SHA}, got: ${ACTUAL_SHA}"
            fi
            info "Checksum verified: ${ACTUAL_SHA:0:16}..."
        fi
    else
        warn "Binary ${BINARY_NAME} not listed in checksums.txt. Proceeding without checksum check."
    fi
    cd - >/dev/null
else
    warn "Could not download checksums.txt. Proceeding without checksum verification."
fi

chmod +x "${TMP_DIR}/${BINARY_NAME}"

# 6. Determine Destination Directory
INSTALL_DIR=""
if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
elif [ -d "$HOME/.local/bin" ] || mkdir -p "$HOME/.local/bin" 2>/dev/null; then
    INSTALL_DIR="$HOME/.local/bin"
elif [ -d "$HOME/bin" ] || mkdir -p "$HOME/bin" 2>/dev/null; then
    INSTALL_DIR="$HOME/bin"
else
    INSTALL_DIR="/usr/local/bin"
fi

TARGET_BIN="${INSTALL_DIR}/vessel"
if [ "${OS}" = "windows" ]; then
    TARGET_BIN="${INSTALL_DIR}/vessel.exe"
fi

info "Installing binary to ${TARGET_BIN}..."
if [ -w "${INSTALL_DIR}" ]; then
    mv "${TMP_DIR}/${BINARY_NAME}" "${TARGET_BIN}"
else
    info "Elevated permissions required to install to ${INSTALL_DIR}."
    sudo mv "${TMP_DIR}/${BINARY_NAME}" "${TARGET_BIN}"
fi

# 7. Verification & Path Notice
success "Vessel ${TARGET_VERSION} installed successfully!"

if ! command -v vessel >/dev/null 2>&1; then
    echo
    warn "${INSTALL_DIR} is not currently in your \$PATH."
    printf "To use 'vessel' from anywhere, add this to your ~/.bashrc or ~/.zshrc:\n\n"
    printf "    ${BOLD}export PATH=\"%s:\$PATH\"${NC}\n\n" "${INSTALL_DIR}"
fi

echo
printf "Run ${BOLD}vessel --help${NC} to get started.\n"

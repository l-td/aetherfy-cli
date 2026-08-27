#!/bin/bash
#
# Aetherfy CLI Installer
#
# Usage:
#   curl -fsSL https://aetherfy.com/install.sh | bash
#
# THIS FILE IS SERVED AT THAT URL. aetherfy.com/install.sh is a temporary (307)
# redirect to this file's raw copy on main, configured in the OTHER repository:
#   aetherfy-dashboard:landing/next.config.js  (redirects(), source '/install.sh')
# There is no second copy — that redirect points here, so editing this file
# changes what users curl, on the next push to main and with no deploy. If you
# need to move or rename it, fix the redirect in the same change.
#
# No releases are tagged yet, so the download below has nothing to fetch and
# fails loudly with the URL it tried. That is deliberate and expires with the
# first tag; keep this script working and correct in the meantime.
#
# Linux and macOS only. Windows is not supported by this script: Git Bash has
# no sudo and no /usr/local/bin, and an extensionless binary is useless to cmd
# or PowerShell. On Windows, download the release zip from
# https://github.com/l-td/aetherfy-cli/releases or build from source (README).
#
# Environment variables:
#   AETHERFY_INSTALL_DIR - Installation directory (default: /usr/local/bin)
#   AETHERFY_VERSION     - Specific version to install (default: latest).
#                          Accepts "0.1.0" or "v0.1.0"; both resolve to the
#                          same release.
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BINARY_NAME="afy"
GITHUB_REPO="l-td/aetherfy-cli"
INSTALL_DIR="${AETHERFY_INSTALL_DIR:-/usr/local/bin}"
VERSION="${AETHERFY_VERSION:-latest}"
# Tags are vX.Y.Z; accept the version with or without the leading v.
VERSION="${VERSION#v}"

# Detect OS and architecture
detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        linux)   OS="linux" ;;
        darwin)  OS="darwin" ;;
        mingw*|msys*|cygwin*)
            echo -e "${RED}Error: this installer supports Linux and macOS only.${NC}"
            echo "On Windows, download the release zip from:"
            echo "  https://github.com/${GITHUB_REPO}/releases"
            echo "or build from source (see the README)."
            exit 1
            ;;
        *)
            echo -e "${RED}Error: Unsupported operating system: $OS${NC}"
            exit 1
            ;;
    esac

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *)
            echo -e "${RED}Error: Unsupported architecture: $ARCH${NC}"
            exit 1
            ;;
    esac

    PLATFORM="${OS}-${ARCH}"
    echo -e "${BLUE}Detected platform: ${PLATFORM}${NC}"
}

# Resolve the release URL prefix. Deliberately no api.github.com call: the
# /releases/latest/download redirect serves the newest asset directly, which
# costs us neither the anonymous rate limit nor a JSON parse that can only
# fail silently.
resolve_release() {
    if [ "$VERSION" = "latest" ]; then
        URL_PREFIX="https://github.com/${GITHUB_REPO}/releases/latest/download"
        echo -e "${BLUE}Installing version: latest${NC}"
    else
        URL_PREFIX="https://github.com/${GITHUB_REPO}/releases/download/v${VERSION}"
        echo -e "${BLUE}Installing version: v${VERSION}${NC}"
    fi
}

# Fetch $1 into $2. Say which URL failed: under `set -e` a bare non-zero curl
# kills the script with no output whatsoever, and a 404 here — wrong tag, asset
# renamed, release not published yet — is the likeliest way this ever fails.
download() {
    if command -v curl &> /dev/null; then
        if ! curl -fsSL "$1" -o "$2"; then
            echo -e "${RED}Error: download failed: $1${NC}"
            exit 1
        fi
    elif command -v wget &> /dev/null; then
        if ! wget -q "$1" -O "$2"; then
            echo -e "${RED}Error: download failed: $1${NC}"
            exit 1
        fi
    else
        echo -e "${RED}Error: curl or wget is required${NC}"
        exit 1
    fi
}

# Check the archive against checksums.txt. Fails closed: no checksum tool
# means no install, never an unverified one.
verify_checksum() {
    echo -e "${BLUE}Verifying checksum...${NC}"

    if ! grep " ${ASSET}\$" checksums.txt > "${ASSET}.sha256"; then
        echo -e "${RED}Error: ${ASSET} is not listed in checksums.txt${NC}"
        exit 1
    fi

    if command -v sha256sum &> /dev/null; then
        CHECK_CMD="sha256sum -c"
    elif command -v shasum &> /dev/null; then
        CHECK_CMD="shasum -a 256 -c"
    else
        echo -e "${RED}Error: sha256sum or shasum is required to verify the download${NC}"
        echo "Refusing to install an unverified binary."
        exit 1
    fi

    if ! $CHECK_CMD "${ASSET}.sha256"; then
        echo -e "${RED}Error: checksum verification failed for ${ASSET}${NC}"
        exit 1
    fi
}

# Download and install
install() {
    ASSET="${BINARY_NAME}-${PLATFORM}.tar.gz"
    DOWNLOAD_URL="${URL_PREFIX}/${ASSET}"

    echo -e "${BLUE}Downloading from: ${DOWNLOAD_URL}${NC}"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap "rm -rf ${TMP_DIR}" EXIT

    # Under its real name: checksums.txt lists the asset by that name, and the
    # -c check matches on the filename in the line.
    download "${DOWNLOAD_URL}" "${TMP_DIR}/${ASSET}"
    download "${URL_PREFIX}/checksums.txt" "${TMP_DIR}/checksums.txt"

    cd "${TMP_DIR}"

    # Verify before extracting, never after
    verify_checksum

    # Extract
    tar xzf "${ASSET}"

    if [ ! -f "./${BINARY_NAME}" ]; then
        echo -e "${RED}Error: binary not found in archive${NC}"
        exit 1
    fi

    # Install
    echo -e "${BLUE}Installing to ${INSTALL_DIR}/${BINARY_NAME}...${NC}"

    if [ -w "$INSTALL_DIR" ]; then
        mv "./${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    else
        echo -e "${YELLOW}Requesting sudo access to install to ${INSTALL_DIR}...${NC}"
        sudo mv "./${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
        sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    fi
}

# Verify installation
verify() {
    # Success is what the binary we just installed reports, by its full path.
    # `command -v` would answer about some other afy already on PATH.
    echo ""
    if ! "${INSTALL_DIR}/${BINARY_NAME}" version; then
        echo -e "${RED}Error: ${INSTALL_DIR}/${BINARY_NAME} did not run${NC}"
        exit 1
    fi

    echo ""
    echo -e "${GREEN}✓ Aetherfy CLI installed successfully!${NC}"
    echo ""
    echo -e "${BLUE}Get started:${NC}"
    echo "  afy login              # Authenticate with your API key"
    echo "  afy agents list        # List your agents"
    echo "  afy deploy             # Deploy your agent"
    echo ""
    echo -e "${BLUE}Documentation: https://docs.aetherfy.com${NC}"

    # command -v decides one thing only: whether the PATH hint is needed.
    if ! command -v "$BINARY_NAME" &> /dev/null; then
        echo ""
        echo -e "${YELLOW}Warning: '${BINARY_NAME}' not found in PATH${NC}"
        echo "You may need to add ${INSTALL_DIR} to your PATH:"
        echo ""
        echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
        echo ""
    fi
}

# Main
main() {
    echo ""
    echo -e "${GREEN}╔═══════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║     Aetherfy CLI Installer            ║${NC}"
    echo -e "${GREEN}╚═══════════════════════════════════════╝${NC}"
    echo ""

    detect_platform
    resolve_release
    install
    verify
}

main "$@"

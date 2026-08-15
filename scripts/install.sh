#!/bin/bash
#
# Aetherfy CLI Installer
#
# Usage:
#   curl -fsSL https://aetherfy.com/install.sh | bash
#
# Environment variables:
#   AETHERFY_INSTALL_DIR - Installation directory (default: /usr/local/bin)
#   AETHERFY_VERSION     - Specific version to install (default: latest)
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
GITHUB_REPO="aetherfy/cli"
INSTALL_DIR="${AETHERFY_INSTALL_DIR:-/usr/local/bin}"
VERSION="${AETHERFY_VERSION:-latest}"

# Detect OS and architecture
detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        linux)   OS="linux" ;;
        darwin)  OS="darwin" ;;
        mingw*|msys*|cygwin*) OS="windows" ;;
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

# Get the latest version from GitHub
get_latest_version() {
    if [ "$VERSION" = "latest" ]; then
        echo -e "${BLUE}Fetching latest version...${NC}"
        VERSION=$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')
        if [ -z "$VERSION" ]; then
            echo -e "${RED}Error: Failed to fetch latest version${NC}"
            exit 1
        fi
    fi
    echo -e "${BLUE}Installing version: ${VERSION}${NC}"
}

# Download and install
install() {
    # Construct download URL
    EXTENSION="tar.gz"
    if [ "$OS" = "windows" ]; then
        EXTENSION="zip"
    fi

    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/v${VERSION}/${BINARY_NAME}-${PLATFORM}.${EXTENSION}"

    echo -e "${BLUE}Downloading from: ${DOWNLOAD_URL}${NC}"

    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap "rm -rf ${TMP_DIR}" EXIT

    # Download
    if command -v curl &> /dev/null; then
        curl -fsSL "${DOWNLOAD_URL}" -o "${TMP_DIR}/afy.${EXTENSION}"
    elif command -v wget &> /dev/null; then
        wget -q "${DOWNLOAD_URL}" -O "${TMP_DIR}/afy.${EXTENSION}"
    else
        echo -e "${RED}Error: curl or wget is required${NC}"
        exit 1
    fi

    # Extract
    cd "${TMP_DIR}"
    if [ "$EXTENSION" = "tar.gz" ]; then
        tar xzf "afy.${EXTENSION}"
    else
        unzip -q "afy.${EXTENSION}"
    fi

    # Find binary
    BINARY_FILE=$(find . -name "${BINARY_NAME}*" -type f | head -1)
    if [ -z "$BINARY_FILE" ]; then
        echo -e "${RED}Error: Binary not found in archive${NC}"
        exit 1
    fi

    # Install
    echo -e "${BLUE}Installing to ${INSTALL_DIR}/${BINARY_NAME}...${NC}"

    if [ -w "$INSTALL_DIR" ]; then
        mv "$BINARY_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
        chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    else
        echo -e "${YELLOW}Requesting sudo access to install to ${INSTALL_DIR}...${NC}"
        sudo mv "$BINARY_FILE" "${INSTALL_DIR}/${BINARY_NAME}"
        sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    fi
}

# Verify installation
verify() {
    if command -v "$BINARY_NAME" &> /dev/null; then
        echo -e "${GREEN}✓ Aetherfy CLI installed successfully!${NC}"
        echo ""
        "${BINARY_NAME}" version
        echo ""
        echo -e "${BLUE}Get started:${NC}"
        echo "  afy login              # Authenticate with your API key"
        echo "  afy agents list        # List your agents"
        echo "  afy deploy             # Deploy your agent"
        echo ""
        echo -e "${BLUE}Documentation: https://docs.aetherfy.com${NC}"
    else
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
    get_latest_version
    install
    verify
}

main "$@"

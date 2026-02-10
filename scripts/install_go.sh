#!/bin/bash

# Color definitions
GREEN="\033[0;32m"
YELLOW="\033[0;33m"
RED="\033[0;31m"
NC="\033[0m" # No Color

# Configuration
GOVER=1.23.8

# Function to print success messages
print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

# Function to print error messages and exit
print_error() {
    echo -e "${RED}✗ ERROR: $1${NC}"
    exit 1
}

# Function to check command success
check_success() {
    if [ $? -ne 0 ]; then
        print_error "$1"
    fi
}

install_go() {
    ARCH=$(uname -m)
    if [ "$ARCH" = "aarch64" ]; then
        ARCH="arm64"
    elif [ "$ARCH" = "x86_64" ]; then
        ARCH="amd64"
    else
        print_error "Unsupported architecture: $ARCH"
    fi

    GOFILE="go${GOVER}.linux-${ARCH}.tar.gz"
    GOURL="https://mirrors.aliyun.com/golang/${GOFILE}"

    echo "Downloading Go $GOVER from Aliyun..."
    wget -q --show-progress -O "${GOFILE}" "${GOURL}"
    check_success "Failed to download Go $GOVER"

    echo "Installing Go $GOVER..."
    tar -C /usr/local -xzf "${GOFILE}"
    check_success "Failed to install Go $GOVER (need write permission to /usr/local?)"

    rm -f "${GOFILE}"
    check_success "Failed to clean up Go installation file"

    print_success "Go $GOVER installed successfully"
}


# Check if Go is already installed
if command -v go &> /dev/null; then
    GO_VERSION=$(go version | awk '{print $3}')
    if [[ "$GO_VERSION" == "go$GOVER" ]]; then
        echo -e "${YELLOW}Go $GOVER is already installed. Skipping...${NC}"
    else
        echo -e "${YELLOW}Found Go $GO_VERSION. Will install Go $GOVER...${NC}"
        install_go
    fi
else
    install_go
fi
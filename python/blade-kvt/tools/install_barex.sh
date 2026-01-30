#!/bin/bash
set -e

DEB_PACKAGE_PATH="./tools/accl-barex-cuda12.2-devel-1.5.2-2-tcp.deb"
if [ ! -f "$DEB_PACKAGE_PATH" ]; then
    echo "Error: Package file '$DEB_PACKAGE_PATH' not found."
    exit 1
fi
apt remove -y accl-barex-cuda || true 

echo "Installing barex cuda12.2-devel package..."
dpkg -i "$DEB_PACKAGE_PATH"

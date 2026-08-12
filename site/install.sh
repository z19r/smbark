#!/bin/sh
set -e

REPO="z19r/smbark"
INSTALL_DIR="/usr/local/bin"

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac

os=$(uname -s | tr '[:upper:]' '[:lower:]')
if [ "$os" != "linux" ]; then
  echo "Unsupported OS: $os (smbark is Linux-only)" >&2
  exit 1
fi

url="https://github.com/${REPO}/releases/latest/download/smbark-${os}-${arch}"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

echo "Downloading smbark for ${os}/${arch}..."
curl -fSL -o "$tmp" "$url"
chmod +x "$tmp"

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmp" "${INSTALL_DIR}/smbark"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "$tmp" "${INSTALL_DIR}/smbark"
fi

echo "smbark installed to ${INSTALL_DIR}/smbark"

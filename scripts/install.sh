#!/bin/bash
set -euo pipefail

# OpenTrace Server Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/adham90/opentrace/main/scripts/install.sh | bash

REPO="adham90/opentrace"
BINARY_NAME="opentrace"
INSTALL_DIR="/usr/local/bin"
DATA_DIR="/var/lib/opentrace"
SERVICE_NAME="opentrace"

echo ""
echo "  OpenTrace — Observability for AI coding agents"
echo ""

# ── Detect OS and architecture ───────────────────────────────

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "ERROR: Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
  echo "ERROR: Unsupported OS: $OS (only linux and darwin are supported)"
  exit 1
fi

echo "==> Detected ${OS}/${ARCH}"

# ── Download binary ──────────────────────────────────────────

echo "==> Downloading latest release..."

LATEST_URL="https://github.com/${REPO}/releases/latest"
VERSION=""
if command -v curl &>/dev/null; then
  VERSION=$(curl -fsSL -o /dev/null -w '%{url_effective}' "${LATEST_URL}" 2>/dev/null | grep -oE '[^/]+$' | sed 's/^v//')
elif command -v wget &>/dev/null; then
  VERSION=$(wget --spider -S -O /dev/null "${LATEST_URL}" 2>&1 | grep -i 'Location:' | tail -1 | grep -oE '[^/]+$' | sed 's/^v//')
fi

if [ -z "$VERSION" ]; then
  echo "ERROR: Could not determine latest version."
  echo "Download manually from: https://github.com/${REPO}/releases"
  exit 1
fi

echo "    Version: v${VERSION}"

ARCHIVE="${BINARY_NAME}_${VERSION}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ARCHIVE}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

if command -v curl &>/dev/null; then
  curl -fsSL -o "${TMPDIR}/${ARCHIVE}" "${DOWNLOAD_URL}" || {
    echo "ERROR: Download failed. Check https://github.com/${REPO}/releases"
    exit 1
  }
elif command -v wget &>/dev/null; then
  wget -q -O "${TMPDIR}/${ARCHIVE}" "${DOWNLOAD_URL}" || {
    echo "ERROR: Download failed. Check https://github.com/${REPO}/releases"
    exit 1
  }
else
  echo "ERROR: Neither curl nor wget found. Install one and retry."
  exit 1
fi

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}" "${BINARY_NAME}" 2>/dev/null || {
  echo "ERROR: Failed to extract archive."
  exit 1
}

# ── Install binary ───────────────────────────────────────────

echo "==> Installing to ${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${TMPDIR}/${BINARY_NAME}"

if [ "$(id -u)" -eq 0 ]; then
  mv "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
else
  sudo mv "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
fi

if "${INSTALL_DIR}/${BINARY_NAME}" version &>/dev/null; then
  echo "    $("${INSTALL_DIR}/${BINARY_NAME}" version)"
fi

# ── Initialize database ──────────────────────────────────────

echo "==> Initializing database..."

if [ "$(id -u)" -eq 0 ]; then
  mkdir -p "${DATA_DIR}"
else
  sudo mkdir -p "${DATA_DIR}"
  sudo chown "$(id -u):$(id -g)" "${DATA_DIR}"
fi

OPENTRACE_DATA_DIR="${DATA_DIR}" "${INSTALL_DIR}/${BINARY_NAME}" init 2>/dev/null || true

# ── Ask for domain (optional, for Caddy HTTPS) ──────────────

DOMAIN=""
echo ""
echo "  Do you have a domain pointing to this server?"
echo "  (e.g., opentrace.yourcompany.com)"
echo ""
read -r -p "  Domain (leave empty to skip HTTPS): " DOMAIN

# ── Install Caddy for HTTPS (if domain provided) ────────────

CONNECT_URL=""
if [ -n "$DOMAIN" ] && [ "$OS" = "linux" ]; then
  echo "==> Installing Caddy for automatic HTTPS..."

  # Install Caddy via official package repo
  if command -v apt-get &>/dev/null; then
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https >/dev/null 2>&1 || true
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null || true
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null 2>&1 || true
    apt-get update -qq >/dev/null 2>&1 || true
    apt-get install -y caddy >/dev/null 2>&1 || true
  elif command -v dnf &>/dev/null; then
    dnf install -y 'dnf-command(copr)' >/dev/null 2>&1 || true
    dnf copr enable -y @caddy/caddy >/dev/null 2>&1 || true
    dnf install -y caddy >/dev/null 2>&1 || true
  fi

  if command -v caddy &>/dev/null; then
    # Write Caddyfile
    cat > /etc/caddy/Caddyfile <<CADDY
${DOMAIN} {
    reverse_proxy 127.0.0.1:8080
}
CADDY

    systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || true
    echo "    Caddy configured for ${DOMAIN} with automatic HTTPS"
    CONNECT_URL="https://${DOMAIN}"
  else
    echo "    WARNING: Could not install Caddy. Set up a reverse proxy manually."
  fi
fi

# ── Configure firewall ───────────────────────────────────────

if command -v ufw &>/dev/null; then
  echo "==> Configuring firewall..."
  if [ "$(id -u)" -eq 0 ]; then
    ufw allow 22/tcp comment "SSH" >/dev/null 2>&1 || true
    ufw allow 80/tcp comment "HTTP" >/dev/null 2>&1 || true
    ufw allow 443/tcp comment "HTTPS" >/dev/null 2>&1 || true
  else
    sudo ufw allow 22/tcp comment "SSH" >/dev/null 2>&1 || true
    sudo ufw allow 80/tcp comment "HTTP" >/dev/null 2>&1 || true
    sudo ufw allow 443/tcp comment "HTTPS" >/dev/null 2>&1 || true
  fi
  # Note: port 8080 is NOT opened — OpenTrace listens on localhost only
fi

# ── Create systemd service (Linux) ───────────────────────────

if [ "$OS" = "linux" ] && command -v systemctl &>/dev/null; then
  echo "==> Creating systemd service..."

  SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
  SERVICE_CONTENT="[Unit]
Description=OpenTrace Server
After=network.target

[Service]
Type=simple
Environment=OPENTRACE_DATA_DIR=${DATA_DIR}
Environment=OPENTRACE_LISTEN_ADDR=127.0.0.1:8080
ExecStart=${INSTALL_DIR}/${BINARY_NAME} serve
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target"

  if [ "$(id -u)" -eq 0 ]; then
    echo "$SERVICE_CONTENT" > "$SERVICE_FILE"
    systemctl daemon-reload
    systemctl enable --now "$SERVICE_NAME"
  else
    echo "$SERVICE_CONTENT" | sudo tee "$SERVICE_FILE" > /dev/null
    sudo systemctl daemon-reload
    sudo systemctl enable --now "$SERVICE_NAME"
  fi

  echo "    Service ${SERVICE_NAME} started (localhost:8080)"
fi

# ── Detect public IP (fallback if no domain) ─────────────────

if [ -z "$CONNECT_URL" ]; then
  PUBLIC_IP=""
  if command -v curl &>/dev/null; then
    PUBLIC_IP=$(curl -fsSL --connect-timeout 3 ifconfig.me 2>/dev/null || true)
  elif command -v wget &>/dev/null; then
    PUBLIC_IP=$(wget -qO- --timeout=3 ifconfig.me 2>/dev/null || true)
  fi

  if [ -z "$PUBLIC_IP" ]; then
    PUBLIC_IP="YOUR_SERVER_IP"
  fi

  CONNECT_URL="http://${PUBLIC_IP}:8080"
  echo ""
  echo "  WARNING: No domain configured — running without HTTPS."
  echo "  Tokens will be sent in plaintext. Set up a reverse proxy"
  echo "  (Caddy, nginx, Cloudflare Tunnel) for production use."
fi

# ── Done ─────────────────────────────────────────────────────

echo ""
echo "============================================"
echo ""
echo "  OpenTrace installed and running"
echo ""
echo "  Connect from your project directory:"
echo ""
echo "    curl -s ${CONNECT_URL}/connect | bash"
echo ""
echo "  First connection creates your admin account."
echo "  No client install needed — just curl."
echo ""
echo "============================================"
echo ""

if [ "$OS" = "linux" ] && command -v systemctl &>/dev/null; then
  echo "Manage the service:"
  echo "  systemctl status ${SERVICE_NAME}"
  echo "  journalctl -u ${SERVICE_NAME} -f"
  echo ""
fi

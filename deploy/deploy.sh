#!/usr/bin/env bash
set -euo pipefail

# ─── OpenTrace One-Click Deploy to Hetzner ───
#
# Prerequisites:
#   brew install hcloud
#   hcloud context create opentrace  (enter your Hetzner API token)
#
# Usage:
#   ./deploy/deploy.sh --domain example.com --llm-provider anthropic --anthropic-api-key sk-ant-xxx
#   ./deploy/deploy.sh --domain example.com --llm-provider openai --openai-api-key sk-xxx
#   ./deploy/deploy.sh --help

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ─── Defaults ───
SERVER_NAME="opentrace"
SERVER_TYPE="cx23"        # 2 vCPU, 4GB RAM (~€4/mo)
IMAGE="ubuntu-24.04"
LOCATION="fsn1"           # Falkenstein, Germany
DOMAIN=""
LLM_PROVIDER="anthropic"
ANTHROPIC_API_KEY=""
ANTHROPIC_MODEL="claude-sonnet-4-5-20250929"
OPENAI_API_KEY=""
OPENAI_MODEL="gpt-4o"
GEMINI_API_KEY=""
GEMINI_MODEL="gemini-2.5-flash-preview-04-17"
APP_API_KEY=""
SSH_KEY=""

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Required:
  --domain <domain>              Domain name pointing to the server (e.g. trace.example.com)

LLM Provider (pick one):
  --llm-provider <provider>      LLM provider: anthropic, openai, gemini, ollama (default: anthropic)
  --anthropic-api-key <key>      Anthropic API key (required if provider is anthropic)
  --anthropic-model <model>      Anthropic model (default: claude-sonnet-4-5-20250929)
  --openai-api-key <key>         OpenAI API key (required if provider is openai)
  --openai-model <model>         OpenAI model (default: gpt-4o)
  --gemini-api-key <key>         Gemini API key (required if provider is gemini)
  --gemini-model <model>         Gemini model (default: gemini-2.5-flash-preview-04-17)

Optional:
  --server-name <name>           Server name (default: opentrace)
  --server-type <type>           Hetzner server type (default: cx23)
  --location <loc>               Hetzner location: fsn1, nbg1, hel1 (default: fsn1)
  --app-api-key <key>            Bearer token to protect the API (auto-generated if empty)
  --ssh-key <name>               SSH key name in Hetzner (uses first available if empty)
  --help                         Show this help

Examples:
  $(basename "$0") --domain trace.example.com --anthropic-api-key sk-ant-xxx
  $(basename "$0") --domain trace.example.com --llm-provider openai --openai-api-key sk-xxx
  $(basename "$0") --domain trace.example.com --llm-provider gemini --gemini-api-key AIza...
EOF
  exit 0
}

# ─── Parse arguments ───
while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)             DOMAIN="$2"; shift 2 ;;
    --llm-provider)       LLM_PROVIDER="$2"; shift 2 ;;
    --anthropic-api-key)  ANTHROPIC_API_KEY="$2"; shift 2 ;;
    --anthropic-model)    ANTHROPIC_MODEL="$2"; shift 2 ;;
    --openai-api-key)     OPENAI_API_KEY="$2"; shift 2 ;;
    --openai-model)       OPENAI_MODEL="$2"; shift 2 ;;
    --gemini-api-key)     GEMINI_API_KEY="$2"; shift 2 ;;
    --gemini-model)       GEMINI_MODEL="$2"; shift 2 ;;
    --app-api-key)        APP_API_KEY="$2"; shift 2 ;;
    --server-name)        SERVER_NAME="$2"; shift 2 ;;
    --server-type)        SERVER_TYPE="$2"; shift 2 ;;
    --location)           LOCATION="$2"; shift 2 ;;
    --ssh-key)            SSH_KEY="$2"; shift 2 ;;
    --help)               usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
done

# ─── Validate ───
if [[ -z "$DOMAIN" ]]; then
  echo "Error: --domain is required"
  echo "Run with --help for usage"
  exit 1
fi

if ! command -v hcloud &>/dev/null; then
  echo "Error: hcloud CLI not found. Install it with: brew install hcloud"
  exit 1
fi

if [[ "$LLM_PROVIDER" == "anthropic" && -z "$ANTHROPIC_API_KEY" ]]; then
  echo "Error: --anthropic-api-key is required when using anthropic provider"
  exit 1
fi

if [[ "$LLM_PROVIDER" == "openai" && -z "$OPENAI_API_KEY" ]]; then
  echo "Error: --openai-api-key is required when using openai provider"
  exit 1
fi

if [[ "$LLM_PROVIDER" == "gemini" && -z "$GEMINI_API_KEY" ]]; then
  echo "Error: --gemini-api-key is required when using gemini provider"
  exit 1
fi

# Auto-generate app API key if not provided
if [[ -z "$APP_API_KEY" ]]; then
  APP_API_KEY="$(openssl rand -hex 32)"
  echo "Generated API key: $APP_API_KEY"
  echo "(save this — you'll need it to access the API)"
fi

# Find SSH key
if [[ -z "$SSH_KEY" ]]; then
  SSH_KEY="$(hcloud ssh-key list -o noheader -o columns=name | head -1 | xargs)"
  if [[ -z "$SSH_KEY" ]]; then
    echo "Error: No SSH keys found in Hetzner. Add one with:"
    echo "  hcloud ssh-key create --name mykey --public-key-from-file ~/.ssh/id_ed25519.pub"
    exit 1
  fi
  echo "Using SSH key: $SSH_KEY"
fi

# ─── Generate cloud-init from template ───
CLOUD_INIT=$(cat "$SCRIPT_DIR/cloud-init.yml")
CLOUD_INIT="${CLOUD_INIT//\$\{DOMAIN\}/$DOMAIN}"
CLOUD_INIT="${CLOUD_INIT//\$\{LLM_PROVIDER\}/$LLM_PROVIDER}"
CLOUD_INIT="${CLOUD_INIT//\$\{ANTHROPIC_API_KEY\}/$ANTHROPIC_API_KEY}"
CLOUD_INIT="${CLOUD_INIT//\$\{ANTHROPIC_MODEL\}/$ANTHROPIC_MODEL}"
CLOUD_INIT="${CLOUD_INIT//\$\{OPENAI_API_KEY\}/$OPENAI_API_KEY}"
CLOUD_INIT="${CLOUD_INIT//\$\{OPENAI_MODEL\}/$OPENAI_MODEL}"
CLOUD_INIT="${CLOUD_INIT//\$\{GEMINI_API_KEY\}/$GEMINI_API_KEY}"
CLOUD_INIT="${CLOUD_INIT//\$\{GEMINI_MODEL\}/$GEMINI_MODEL}"
CLOUD_INIT="${CLOUD_INIT//\$\{APP_API_KEY\}/$APP_API_KEY}"

TMPFILE=$(mktemp)
echo "$CLOUD_INIT" > "$TMPFILE"

# ─── Create server ───
echo ""
echo "Creating Hetzner server..."
echo "  Name:     $SERVER_NAME"
echo "  Type:     $SERVER_TYPE"
echo "  Location: $LOCATION"
echo "  Image:    $IMAGE"
echo "  Domain:   $DOMAIN"
echo ""

hcloud server create \
  --name "$SERVER_NAME" \
  --type "$SERVER_TYPE" \
  --image "$IMAGE" \
  --location "$LOCATION" \
  --ssh-key "$SSH_KEY" \
  --user-data-from-file "$TMPFILE"

rm -f "$TMPFILE"

SERVER_IP=$(hcloud server ip "$SERVER_NAME" 2>/dev/null || echo "")

echo ""
echo "================================================"
echo "  Server created successfully!"
echo "================================================"
echo ""
echo "  IP Address:  $SERVER_IP"
echo "  Domain:      $DOMAIN"
echo "  SSH:         ssh root@$SERVER_IP"
echo ""
echo "  Next steps:"
echo "  1. Point DNS A record for $DOMAIN -> $SERVER_IP"
echo "  2. Wait 3-5 minutes for cloud-init to finish"
echo "  3. Visit https://$DOMAIN"
echo ""
echo "  Monitor setup progress:"
echo "    ssh root@$SERVER_IP tail -f /var/log/cloud-init-output.log"
echo ""
echo "  API Key:     $APP_API_KEY"
echo "================================================"

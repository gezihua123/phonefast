#!/bin/bash
# phonefast MCP — one-command install
#   ./scripts/install.sh              → install to /usr/local/bin
#   ./scripts/install.sh --local      → install to ~/.local/bin
#   ./scripts/install.sh --dry-run    → print config, don't install
set -euo pipefail

MODE="${1:---global}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "==> Building phonefast..."
cd "$PROJECT_DIR"
go build -trimpath -ldflags="-s -w" -o phonefast ./cmd/phonefast/
echo "     $(ls -lh phonefast | awk '{print $5}') binary"

case "$MODE" in
  --local)
    mkdir -p "$HOME/.local/bin"
    cp phonefast "$HOME/.local/bin/phonefast"
    INSTALL_PATH="$HOME/.local/bin/phonefast"
    ;;
  --dry-run)
    INSTALL_PATH="$PROJECT_DIR/phonefast"
    echo "==> Dry-run (binary left at $INSTALL_PATH)"
    ;;
  *)
    sudo cp phonefast /usr/local/bin/phonefast
    INSTALL_PATH="/usr/local/bin/phonefast"
    ;;
esac
echo "==> Binary: $INSTALL_PATH"

# ── Server assets ──────────────────────────────────────────────────────────────
ASSET_DIR="$HOME/.phonefast"
mkdir -p "$ASSET_DIR"
cp "$PROJECT_DIR/android/scrcpy-server.jar" "$ASSET_DIR/"
echo "3.3.4" > "$ASSET_DIR/scrcpy-server.version"
echo "==> Assets: $ASSET_DIR/"

# ── Claude Desktop config ──────────────────────────────────────────────────────
CONFIG_DIR="$HOME/Library/Application Support/Claude"
CONFIG_FILE="$CONFIG_DIR/claude_desktop_config.json"

if [ "$MODE" = "--dry-run" ]; then
  echo ""
  echo "Claude Desktop config to add:"
  echo "  File: $CONFIG_FILE"
  echo ""
  echo '{'
  echo '  "mcpServers": {'
  echo '    "phonefast": {'
  echo "      \"command\": \"$INSTALL_PATH\","
  echo '      "args": ["serve", "--transport", "stdio"]'
  echo '    }'
  echo '  }'
  echo '}'
  exit 0
fi

if [ -f "$CONFIG_FILE" ]; then
  echo "==> Existing Claude config found. Merging..."
  python3 "$SCRIPT_DIR/merge-config.py" "$INSTALL_PATH"
else
  mkdir -p "$CONFIG_DIR"
  cat > "$CONFIG_FILE" <<EOF
{
  "mcpServers": {
    "phonefast": {
      "command": "$INSTALL_PATH",
      "args": ["serve", "--transport", "stdio"]
    }
  }
}
EOF
  echo "==> Created $CONFIG_FILE"
fi

echo ""
echo "Done. Restart Claude Desktop to use phonefast MCP tools."
echo "  Tools: screenshot, get_ui_elements, observe, tap, tap_element,"
echo "         swipe, type_text, back, home, press_key, launch_app, wait"

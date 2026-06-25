#!/usr/bin/env python3
"""Merge phonefast into an existing Claude Desktop MCP config."""
import json, sys, os

INSTALL_PATH = sys.argv[1] if len(sys.argv) > 1 else "/usr/local/bin/phonefast"
CONFIG_DIR  = os.path.expanduser("~/Library/Application Support/Claude")
CONFIG_FILE = os.path.join(CONFIG_DIR, "claude_desktop_config.json")

# Read existing
config = {}
if os.path.exists(CONFIG_FILE):
    with open(CONFIG_FILE) as f:
        config = json.load(f)

# Merge phonefast
config.setdefault("mcpServers", {})
config["mcpServers"]["phonefast"] = {
    "command": INSTALL_PATH,
    "args": ["serve", "--transport", "stdio"]
}

# Write back
os.makedirs(CONFIG_DIR, exist_ok=True)
with open(CONFIG_FILE, "w") as f:
    json.dump(config, f, indent=2)

print(f"✓ Merged phonefast into {CONFIG_FILE}")
print(f"  command: {INSTALL_PATH}")

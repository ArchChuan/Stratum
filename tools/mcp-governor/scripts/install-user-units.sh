#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
module_dir=$(cd -- "$script_dir/.." && pwd)
unit_source_dir="$module_dir/systemd"

bin_dir="$HOME/.local/bin"
state_dir="$HOME/.local/state/mcp-governor"
config_dir="$HOME/.config/mcp-governor"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

install -d -m 0700 "$bin_dir" "$state_dir" "$config_dir" "$unit_dir"

tmp_dir=$(mktemp -d)
staged_binary="$bin_dir/.mcp-governor.new.$$"
cleanup() {
  rm -rf -- "$tmp_dir"
  rm -f -- "$staged_binary"
}
trap cleanup EXIT

if [[ -n "${MCP_GOVERNOR_BINARY:-}" ]]; then
  install -m 0755 "$MCP_GOVERNOR_BINARY" "$tmp_dir/mcp-governor"
else
  (
    cd -- "$module_dir"
    go build -o "$tmp_dir/mcp-governor" ./cmd/mcp-governor
  )
fi

install -m 0755 "$tmp_dir/mcp-governor" "$staged_binary"
mv -f -- "$staged_binary" "$bin_dir/mcp-governor"

if [[ ! -e "$config_dir/config.json" ]]; then
  install -m 0600 "$module_dir/config.example.json" "$config_dir/config.json"
fi

install -m 0644 "$unit_source_dir/mcp-governor-observe.service" "$unit_dir/mcp-governor-observe.service"
install -m 0644 "$unit_source_dir/mcp-governor-observe.timer" "$unit_dir/mcp-governor-observe.timer"

"${SYSTEMCTL:-systemctl}" --user daemon-reload
"${SYSTEMCTL:-systemctl}" --user enable --now mcp-governor-observe.timer

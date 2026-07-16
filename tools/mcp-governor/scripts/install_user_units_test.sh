#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
installer="$script_dir/install-user-units.sh"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

export HOME="$tmp_dir/home"
export XDG_CONFIG_HOME="$tmp_dir/xdg-config"
export XDG_STATE_HOME="$tmp_dir/xdg-state"
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$tmp_dir/run-from"
chmod 0711 "$XDG_CONFIG_HOME"
mkdir -p "$HOME/.local/bin" "$HOME/.local/state/mcp-governor" "$XDG_CONFIG_HOME/systemd/user"
chmod 0755 "$HOME/.local/bin" "$XDG_CONFIG_HOME/systemd" "$XDG_CONFIG_HOME/systemd/user"
chmod 0750 "$HOME/.local/state/mcp-governor"

fixture_binary="$tmp_dir/mcp-governor"
printf '#!/bin/sh\nexit 0\n' >"$fixture_binary"
chmod 0755 "$fixture_binary"
export MCP_GOVERNOR_BINARY="$fixture_binary"

systemctl_log="$tmp_dir/systemctl.log"
fake_systemctl="$tmp_dir/systemctl"
cat >"$fake_systemctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$SYSTEMCTL_LOG"
EOF
chmod 0755 "$fake_systemctl"
export SYSTEMCTL="$fake_systemctl"
export SYSTEMCTL_LOG="$systemctl_log"

(cd "$tmp_dir/run-from" && "$installer")

binary="$HOME/.local/bin/mcp-governor"
config="$HOME/.config/mcp-governor/config.json"
unit_dir="$XDG_CONFIG_HOME/systemd/user"
service="$unit_dir/mcp-governor-observe.service"
timer="$unit_dir/mcp-governor-observe.timer"

test -x "$binary"
test "$(stat -c %a "$binary")" = 755
test "$(stat -c %a "$HOME/.local/bin")" = 755
test "$(stat -c %a "$HOME/.local/state/mcp-governor")" = 750
test "$(stat -c %a "$XDG_CONFIG_HOME")" = 711
test "$(stat -c %a "$unit_dir")" = 755
test -f "$config"
test "$(stat -c %a "$config")" = 600
test "$(stat -c %a "$HOME/.config")" = 755
test "$(stat -c %a "$HOME/.config/mcp-governor")" = 700
cmp "$script_dir/../config.example.json" "$config"
test -f "$service"
test -f "$timer"
grep -Fq 'ExecStart=%h/.local/bin/mcp-governor snapshot --config %h/.config/mcp-governor/config.json' "$service"
test "$(cat "$systemctl_log")" = $'--user daemon-reload\n--user enable --now mcp-governor-observe.timer'
! grep -Eq 'enable.*mcp-governor-observe\.service' "$systemctl_log"

printf '{"custom":true}\n' >"$config"
chmod 0640 "$config"
: >"$systemctl_log"
(cd / && "$installer")

test "$(cat "$config")" = '{"custom":true}'
test "$(stat -c %a "$config")" = 640
test "$(stat -c %a "$HOME/.local/bin")" = 755
test "$(stat -c %a "$HOME/.local/state/mcp-governor")" = 750
test "$(stat -c %a "$XDG_CONFIG_HOME")" = 711
test "$(stat -c %a "$unit_dir")" = 755
test "$(cat "$systemctl_log")" = $'--user daemon-reload\n--user enable --now mcp-governor-observe.timer'
test ! -e "$HOME/.config/systemd/user/mcp-governor-observe.service"

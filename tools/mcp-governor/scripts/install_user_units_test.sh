#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
installer="$script_dir/install-user-units.sh"
tmp_dir=$(mktemp -d)
trap 'rm -rf -- "$tmp_dir"' EXIT

export HOME="$tmp_dir/home"
export XDG_CONFIG_HOME="$tmp_dir/xdg-config"
mkdir -m 0700 "$HOME" "$XDG_CONFIG_HOME"

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

bash "$installer"

state_dir="$HOME/.local/state/mcp-governor"
config_dir="$HOME/.config/mcp-governor"
unit_dir="$XDG_CONFIG_HOME/systemd/user"
salt="$config_dir/identity-salt"
catalog="$config_dir/config.json"

test "$(stat -c %s "$salt")" = 32
test "$(stat -c %a "$salt")" = 600
test "$(stat -c %a "$state_dir")" = 700
test "$(stat -c %a "$state_dir/events")" = 700
test "$(stat -c %a "$state_dir/reports")" = 700
test "$(stat -c %a "$config_dir")" = 700
test "$(stat -c %a "$unit_dir")" = 700
test "$(stat -c %a "$catalog")" = 600
cmp "$script_dir/../config.example.json" "$catalog"

for unit in mcp-governor-observe.service mcp-governor-observe.timer \
  mcp-governor-report.service mcp-governor-report.timer; do
  test -f "$unit_dir/$unit"
  test "$(stat -c %a "$unit_dir/$unit")" = 600
done

observe_service="$unit_dir/mcp-governor-observe.service"
report_service="$unit_dir/mcp-governor-report.service"
report_timer="$unit_dir/mcp-governor-report.timer"
grep -Fq 'ExecStart=%h/.local/bin/mcp-governor snapshot --config %h/.config/mcp-governor/config.json' "$observe_service"
grep -Fq 'ExecStart=%h/.local/bin/mcp-governor report-latest --config %h/.config/mcp-governor/config.json' "$report_service"
! grep -Fq '$(' "$report_service"
grep -Fq 'Persistent=true' "$report_timer"
grep -Fq 'OnCalendar=' "$report_timer"
for service in "$observe_service" "$report_service"; do
  grep -Fq 'UMask=0077' "$service"
  grep -Fq 'NoNewPrivileges=yes' "$service"
  grep -Fq 'RestrictAddressFamilies=AF_UNIX' "$service"
  grep -Fq 'LockPersonality=yes' "$service"
done
if grep -Eq '^(PrivateTmp|ProtectSystem|ProtectHome|ReadWritePaths)=' "$observe_service"; then
  printf 'filesystem namespace directives prevent cross-process PSS collection\n' >&2
  exit 1
fi

expected_systemctl=$'--user daemon-reload\n--user enable mcp-governor-observe.timer mcp-governor-report.timer\n--user restart mcp-governor-observe.timer mcp-governor-report.timer'
test "$(cat "$systemctl_log")" = "$expected_systemctl"
! grep -Eq 'enable.*mcp-governor-(observe|report)\.service' "$systemctl_log"

salt_digest=$(sha256sum "$salt")
catalog_digest=$(sha256sum "$catalog")
printf '{"custom":true}\n' >"$catalog"
chmod 0600 "$catalog"
catalog_digest=$(sha256sum "$catalog")
: >"$systemctl_log"
bash "$installer"
test "$(sha256sum "$salt")" = "$salt_digest"
test "$(sha256sum "$catalog")" = "$catalog_digest"
test "$(cat "$systemctl_log")" = "$expected_systemctl"

bad_salt_root="$tmp_dir/bad-salt"
mkdir -m 0700 "$bad_salt_root" "$bad_salt_root/home" "$bad_salt_root/xdg"
mkdir -p "$bad_salt_root/home/.config/mcp-governor"
chmod 0700 "$bad_salt_root/home/.config" "$bad_salt_root/home/.config/mcp-governor"
ln -s /dev/null "$bad_salt_root/home/.config/mcp-governor/identity-salt"
if HOME="$bad_salt_root/home" XDG_CONFIG_HOME="$bad_salt_root/xdg" SYSTEMCTL="$fake_systemctl" \
  SYSTEMCTL_LOG="$bad_salt_root/systemctl.log" MCP_GOVERNOR_BINARY="$fixture_binary" bash "$installer" \
  >"$bad_salt_root/stdout" 2>"$bad_salt_root/stderr"; then
  printf 'symlink salt unexpectedly accepted\n' >&2
  exit 1
fi

bad_unit_root="$tmp_dir/bad-unit"
mkdir -m 0700 "$bad_unit_root" "$bad_unit_root/home" "$bad_unit_root/xdg"
mkdir -p "$bad_unit_root/xdg/systemd/user"
chmod 0700 "$bad_unit_root/xdg/systemd" "$bad_unit_root/xdg/systemd/user"
ln -s /dev/null "$bad_unit_root/xdg/systemd/user/mcp-governor-report.service"
if HOME="$bad_unit_root/home" XDG_CONFIG_HOME="$bad_unit_root/xdg" SYSTEMCTL="$fake_systemctl" \
  SYSTEMCTL_LOG="$bad_unit_root/systemctl.log" MCP_GOVERNOR_BINARY="$fixture_binary" bash "$installer" \
  >"$bad_unit_root/stdout" 2>"$bad_unit_root/stderr"; then
  printf 'symlink unit unexpectedly accepted\n' >&2
  exit 1
fi

bad_parent_root="$tmp_dir/bad-parent"
mkdir -m 0700 "$bad_parent_root" "$bad_parent_root/home" "$bad_parent_root/xdg"
chmod 0770 "$bad_parent_root/xdg"
if HOME="$bad_parent_root/home" XDG_CONFIG_HOME="$bad_parent_root/xdg" SYSTEMCTL="$fake_systemctl" \
  SYSTEMCTL_LOG="$bad_parent_root/systemctl.log" MCP_GOVERNOR_BINARY="$fixture_binary" bash "$installer" \
  >"$bad_parent_root/stdout" 2>"$bad_parent_root/stderr"; then
  printf 'writable unit parent unexpectedly accepted\n' >&2
  exit 1
fi

#!/usr/bin/env bash
set -euo pipefail

umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
module_dir=$(cd -- "$script_dir/.." && pwd)
unit_source_dir="$module_dir/systemd"

bin_dir="$HOME/.local/bin"
state_dir="$HOME/.local/state/mcp-governor"
events_dir="$state_dir/events"
reports_dir="$state_dir/reports"
config_dir="$HOME/.config/mcp-governor"
xdg_config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
unit_dir="$xdg_config_home/systemd/user"

die() {
  printf 'mcp-governor installer: %s\n' "$1" >&2
  exit 1
}

validate_directory() {
  local path=$1
  local required_mode=$2
  local info mode
  [[ ! -L "$path" && -d "$path" ]] || die "unsafe directory path"
  info=$(stat -c '%a' -- "$path") || die "cannot inspect directory"
  mode=$((8#$info))
  (( (mode & 0022) == 0 )) || die "directory is group/world writable"
  if [[ -n "$required_mode" && "$info" != "$required_mode" ]]; then
    die "directory has unexpected permissions"
  fi
}

ensure_directory() {
  local path=$1
  local mode=$2
  if [[ -e "$path" || -L "$path" ]]; then
    validate_directory "$path" "$mode"
    return
  fi
  mkdir -m "$mode" -- "$path" || die "cannot create directory"
  validate_directory "$path" "$mode"
}

ensure_parent_directory() {
  local path=$1
  local mode=$2
  if [[ -e "$path" || -L "$path" ]]; then
    validate_directory "$path" ""
    return
  fi
  mkdir -m "$mode" -- "$path" || die "cannot create parent directory"
  validate_directory "$path" ""
}

validate_private_file() {
  local path=$1
  local size=${2:-}
  [[ ! -L "$path" && -f "$path" ]] || die "unsafe private file"
  [[ "$(stat -c %a -- "$path")" == 600 ]] || die "private file has unexpected permissions"
  if [[ -n "$size" && "$(stat -c %s -- "$path")" != "$size" ]]; then
    die "private file has unexpected size"
  fi
}

install_absent_private() {
  local source=$1
  local destination=$2
  local temp
  if [[ -e "$destination" || -L "$destination" ]]; then
    validate_private_file "$destination"
    return
  fi
  temp=$(mktemp "$(dirname -- "$destination")/.mcp-governor.XXXXXX") || die "cannot stage private file"
  install -m 0600 -- "$source" "$temp" || { rm -f -- "$temp"; die "cannot stage private file"; }
  if ! ln -- "$temp" "$destination" 2>/dev/null; then
    rm -f -- "$temp"
    [[ -e "$destination" || -L "$destination" ]] || die "cannot install private file"
    validate_private_file "$destination"
    return
  fi
  rm -f -- "$temp"
  validate_private_file "$destination"
}

generate_salt_absent() {
  local destination=$1
  local temp
  if [[ -e "$destination" || -L "$destination" ]]; then
    validate_private_file "$destination" 32
    return
  fi
  temp=$(mktemp "$(dirname -- "$destination")/.identity-salt.XXXXXX") || die "cannot stage identity salt"
  if ! dd if=/dev/urandom of="$temp" bs=32 count=1 status=none; then
    rm -f -- "$temp"
    die "cannot generate identity salt"
  fi
  chmod 0600 -- "$temp"
  [[ "$(stat -c %s -- "$temp")" == 32 ]] || { rm -f -- "$temp"; die "identity salt has wrong size"; }
  if ! ln -- "$temp" "$destination" 2>/dev/null; then
    rm -f -- "$temp"
    [[ -e "$destination" || -L "$destination" ]] || die "cannot install identity salt"
    validate_private_file "$destination" 32
    return
  fi
  rm -f -- "$temp"
  validate_private_file "$destination" 32
}

install_unit() {
  local name=$1
  local destination="$unit_dir/$name"
  local temp
  if [[ -e "$destination" || -L "$destination" ]]; then
    [[ ! -L "$destination" && -f "$destination" ]] || die "unsafe unit destination"
  fi
  temp=$(mktemp "$unit_dir/.${name}.XXXXXX") || die "cannot stage unit"
  install -m 0600 -- "$unit_source_dir/$name" "$temp" || { rm -f -- "$temp"; die "cannot stage unit"; }
  mv -fT -- "$temp" "$destination" || { rm -f -- "$temp"; die "cannot install unit"; }
}

ensure_parent_directory "$HOME/.local" 0700
ensure_parent_directory "$bin_dir" 0700
ensure_parent_directory "$HOME/.local/state" 0700
ensure_directory "$state_dir" 700
ensure_directory "$events_dir" 700
ensure_directory "$reports_dir" 700
ensure_parent_directory "$HOME/.config" 0700
ensure_directory "$config_dir" 700
ensure_parent_directory "$xdg_config_home" 0700
ensure_directory "$xdg_config_home/systemd" 700
ensure_directory "$unit_dir" 700

tmp_dir=$(mktemp -d)
staged_binary="$bin_dir/.mcp-governor.new.$$"
cleanup() {
  rm -rf -- "$tmp_dir"
  rm -f -- "$staged_binary"
}
trap cleanup EXIT

if [[ -n "${MCP_GOVERNOR_BINARY:-}" ]]; then
  install -m 0755 -- "$MCP_GOVERNOR_BINARY" "$tmp_dir/mcp-governor"
else
  (cd -- "$module_dir" && go build -o "$tmp_dir/mcp-governor" ./cmd/mcp-governor)
fi
install -m 0755 -- "$tmp_dir/mcp-governor" "$staged_binary"
mv -fT -- "$staged_binary" "$bin_dir/mcp-governor"

install_absent_private "$module_dir/config.example.json" "$config_dir/config.json"
generate_salt_absent "$config_dir/identity-salt"

install_unit mcp-governor-observe.service
install_unit mcp-governor-observe.timer
install_unit mcp-governor-report.service
install_unit mcp-governor-report.timer

systemctl_command="${SYSTEMCTL:-systemctl}"
"$systemctl_command" --user daemon-reload
"$systemctl_command" --user enable mcp-governor-observe.timer mcp-governor-report.timer
"$systemctl_command" --user restart mcp-governor-observe.timer mcp-governor-report.timer

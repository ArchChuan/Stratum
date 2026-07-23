#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
module_dir=$(cd -- "$script_dir/.." && pwd)
tmp_dir=$(mktemp -d)
declare -a child_pids=()
declare -a fake_pids=()
go_bin=${MCP_GOVERNOR_GO:-go}

wait_for_pid() {
  local pid=$1 deadline=$((SECONDS + 10))
  while kill -0 "$pid" 2>/dev/null; do
    if (( SECONDS >= deadline )); then
      return 1
    fi
    sleep 0.1
  done
}

cleanup() {
  local pid stubborn=0
  for pid in "${child_pids[@]}" "${fake_pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  for pid in "${child_pids[@]}" "${fake_pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null && ! wait_for_pid "$pid"; then
      printf 'stubborn E2E process pid=%s\n' "$pid" >&2
      stubborn=1
    fi
    wait "$pid" 2>/dev/null || true
  done
  rm -rf -- "$tmp_dir"
  return "$stubborn"
}
trap cleanup EXIT INT TERM

umask 077
export HOME="$tmp_dir/home"
export XDG_CONFIG_HOME="$tmp_dir/xdg-config"
state_dir="$tmp_dir/state"
config_dir="$tmp_dir/config"
build_dir="$tmp_dir/build"
render_dir="$tmp_dir/rendered"
pid_dir="$tmp_dir/fake-pids"
mkdir -m 0700 "$HOME" "$XDG_CONFIG_HOME" "$state_dir" "$config_dir" "$build_dir" "$render_dir" "$pid_dir"
export MCP_GOVERNOR_E2E_PID_DIR="$pid_dir"

governor="$build_dir/mcp-governor"
fake_server="$build_dir/fake-mcp-server"
(cd "$module_dir" && GOTOOLCHAIN=local "$go_bin" build -o "$governor" ./cmd/mcp-governor)
(cd "$module_dir" && GOTOOLCHAIN=local "$go_bin" build -o "$fake_server" ./testdata/e2e/fake-mcp-server.go)

salt="$config_dir/identity-salt"
head -c 32 /dev/urandom >"$salt"
chmod 0600 "$salt"
catalog="$config_dir/catalog.json"
python3 - "$catalog" "$state_dir" "$salt" "$fake_server" <<'PY'
import json, pathlib, sys
path, state, salt, server = sys.argv[1:]
doc = {
    "version": 2,
    "output_path": f"{state}/snapshot.json",
    "registry_path": f"{state}/registry.json",
    "observation": {
        "events_dir": f"{state}/events",
        "reports_dir": f"{state}/reports",
        "salt_path": salt,
        "raw_retention_days": 30,
    },
    "services": [{
        "name": "fixture",
        "command": server,
        "args": ["observation"],
        "cwd": state,
        "transport": "stdio",
        "scope": "user",
        "session_policy": "isolated",
        "clients": ["codex", "claude", "vscode", "lingma"],
        "all_args_contain": ["fake-mcp-server", "observation"],
    }],
}
pathlib.Path(path).write_text(json.dumps(doc) + "\n")
PY
chmod 0600 "$catalog"

clients=(codex claude vscode lingma)
for client in "${clients[@]}"; do
  extension=json
  [[ "$client" == codex ]] && extension=toml
  "$governor" render-config --config "$catalog" --client "$client" --governor "$governor" \
    --output "$render_dir/$client.$extension"
done

commands="$tmp_dir/commands.json"
python3 - "$render_dir" "$commands" <<'PY'
import json, pathlib, sys, tomllib
root = pathlib.Path(sys.argv[1])
result = {}
codex = tomllib.loads((root / "codex.toml").read_text())
entry = codex["mcp_servers"]["fixture"]
result["codex"] = [entry["command"], *entry["args"]]
for client in ("claude", "vscode", "lingma"):
    doc = json.loads((root / f"{client}.json").read_text())
    servers = doc["servers"] if client == "vscode" else doc["mcpServers"]
    entry = servers["fixture"]
    assert entry.get("type", "stdio") == "stdio"
    result[client] = [entry["command"], *entry["args"]]
pathlib.Path(sys.argv[2]).write_text(json.dumps(result) + "\n")
PY
chmod 0600 "$commands"
printf 'PASS rendered native configs: codex claude vscode lingma\n'

write_input() {
  local path=$1 mode=$2
  case "$mode" in
    success)
      printf '%s\n' \
        '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_only","arguments":{"marker":"DO-NOT-LOG","url":"https://private.invalid"}}}' \
        '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_only","arguments":{"token":"TOKEN=hidden"}}}' >"$path"
      ;;
    cancel)
      printf '%s\n' \
        '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cancelled_read","arguments":{"marker":"DO-NOT-LOG"}}}' \
        '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"SECRET-BODY"}}' \
        '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_only","arguments":{"token":"TOKEN=hidden"}}}' \
        '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_only","arguments":{"url":"https://private.invalid"}}}' >"$path"
      ;;
    disconnect)
      printf '%s\n' \
        '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"abrupt_read","arguments":{"marker":"DO-NOT-LOG","token":"TOKEN=hidden"}}}' >"$path"
      ;;
  esac
  chmod 0600 "$path"
}

launch_client() {
  local client=$1 session=$2 mode=$3
  local input="$tmp_dir/$client.in" output="$tmp_dir/$client.out" error="$tmp_dir/$client.err"
  write_input "$input" "$mode"
  mapfile -d '' -t command < <(python3 - "$commands" "$client" <<'PY'
import json, sys
for value in json.load(open(sys.argv[1]))[sys.argv[2]]:
    sys.stdout.buffer.write(value.encode() + b"\0")
PY
  )
  local separator
  for separator in "${!command[@]}"; do
    [[ "${command[$separator]}" == -- ]] && break
  done
  command=("${command[@]:0:separator}" --session "$session" "${command[@]:separator}")
  "${command[@]}" <"$input" >"$output" 2>"$error" &
  child_pids+=("$!")
}

launch_client codex 1101:2101 success
launch_client claude 1102:2102 cancel
launch_client vscode 1103:2103 success
launch_client lingma 1104:2104 disconnect

pid_deadline=$((SECONDS + 10))
while (( $(find "$pid_dir" -maxdepth 1 -type f | wc -l) < 4 )); do
  if (( SECONDS >= pid_deadline )); then
    printf 'fake MCP servers did not all publish exact PIDs within 10s\n' >&2
    exit 1
  fi
  sleep 0.1
done
while IFS= read -r pid_file; do
  fake_pids+=("$(basename "$pid_file")")
done < <(find "$pid_dir" -maxdepth 1 -type f -print | sort)

process_failure=0
for pid in "${child_pids[@]}"; do
  if ! wait_for_pid "$pid"; then
    printf 'E2E process did not exit within 10s: pid=%s\n' "$pid" >&2
    process_failure=1
    continue
  fi
  if ! wait "$pid"; then
    printf 'E2E client process failed: pid=%s\n' "$pid" >&2
    process_failure=1
  fi
done
(( process_failure == 0 ))
child_pids=()
for pid in "${fake_pids[@]}"; do
  if ! wait_for_pid "$pid"; then
    printf 'fake MCP server did not exit within 10s: pid=%s\n' "$pid" >&2
    exit 1
  fi
done
fake_pids=()

python3 - "$state_dir" <<'PY'
import json, pathlib, stat, sys
root = pathlib.Path(sys.argv[1])
events = []
for path in sorted((root / "events").glob("*/*.jsonl")):
    for line in path.read_text().splitlines():
        event = json.loads(line)
        assert event["kind"] == "tool_call"
        for key in ("client", "service", "tool", "session_hash"):
            assert event.get(key), (path, key, event)
        forbidden = {"params", "result", "error", "arguments", "url", "urls"}
        assert forbidden.isdisjoint(event), (path, event)
        events.append(event)
sessions = len({event["session_hash"] for event in events})
cancelled = sum(event["outcome"] == "cancelled" for event in events)
effective = sum(bool(event.get("effective")) for event in events)
disconnected = sum(event["outcome"] == "disconnected" for event in events)
outcomes = sorted((event["client"], event["outcome"]) for event in events)
concurrency = max((event.get("concurrent_calls", 0) for event in events), default=0)
assert sessions == 4, f"session hash count={sessions}"
assert cancelled == 1, f"cancelled count={cancelled}"
assert effective >= 3, f"effective count={effective}"
assert disconnected == 1, f"disconnected count={disconnected}, outcomes={outcomes}"
assert concurrency >= 2, f"max concurrency={concurrency}"
for path in [root, *root.rglob("*")]:
    mode = stat.S_IMODE(path.stat().st_mode)
    if path.is_dir():
        assert mode & 0o077 == 0, (path, oct(mode))
    elif path.is_file():
        assert mode & 0o177 == 0, (path, oct(mode))
PY
if rg -l 'DO-NOT-LOG|SECRET-BODY|TOKEN=|https?://' "$state_dir" >/dev/null; then
  printf 'sensitive fixture value persisted in state directory\n' >&2
  exit 1
fi
printf 'PASS isolated sessions: 4 unique hashes\n'
printf 'PASS tool outcomes: 1 cancelled, at least 3 effective, 1 disconnected\n'
printf 'PASS metadata privacy and private modes\n'

from=$(date -u -d '1 minute ago' +%Y-%m-%dT%H:%M:%SZ)
to=$(date -u -d '1 minute' +%Y-%m-%dT%H:%M:%SZ)
report="$state_dir/reports/e2e.json"
"$governor" report --config "$catalog" --from "$from" --to "$to" --output "$report" --allow-partial
python3 - "$report" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
clients = {row["client"] for row in doc["tools"]}
assert clients == {"codex", "claude", "vscode", "lingma"}, clients
assert all(row["service"] == "fixture" for row in doc["tools"])
PY
printf 'PASS four-client report split\n'
printf 'PASS child processes exited within 10s\n'

#!/usr/bin/env bash
set -euo pipefail

# WSL -> Windows toast notification with session context.
# Usage: echo '<hook-json>' | notify-windows.sh [title-template] [body-template]
# Templates support: {project} {session} {event} {message} {tool}

title_template="${1:-Codex 通知}"
body_template="${2:-{project} · {message}}"

payload="$(cat || true)"

cwd="$(
  printf '%s' "$payload" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('cwd',''))" 2>/dev/null || true
)"
session_id="$(
  printf '%s' "$payload" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('session_id',''))" 2>/dev/null || true
)"
event="$(
  printf '%s' "$payload" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('event') or d.get('hook_event_name') or d.get('notification_type') or d.get('type') or '')" 2>/dev/null || true
)"
message="$(
  printf '%s' "$payload" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('message') or d.get('reason') or d.get('prompt') or d.get('status') or '')" 2>/dev/null || true
)"
tool="$(
  printf '%s' "$payload" | python3 -c "import sys,json; d=json.load(sys.stdin); ti=d.get('tool_input') or {}; print(d.get('tool_name') or d.get('tool') or ti.get('name') or '')" 2>/dev/null || true
)"

[ -z "$cwd" ] && cwd="$PWD"
[ -z "$event" ] && event="notification"
[ -z "$message" ] && message="需要你确认"
[ -z "$tool" ] && tool="-"

project="$(basename "$cwd")"
session_short="${session_id:0:6}"
[ -z "$session_short" ] && session_short="----"

title="${title_template//\{project\}/$project}"
title="${title//\{session\}/$session_short}"
title="${title//\{event\}/$event}"
title="${title//\{message\}/$message}"
title="${title//\{tool\}/$tool}"

body="${body_template//\{project\}/$project}"
body="${body//\{session\}/$session_short}"
body="${body//\{event\}/$event}"
body="${body//\{message\}/$message}"
body="${body//\{tool\}/$tool}"

ps_title="${title//\'/\'\'}"
ps_body="${body//\'/\'\'}"

powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden \
  -Command "New-BurntToastNotification -Text '$ps_title', '$ps_body'" \
  2>/dev/null &

#!/usr/bin/env bash
set -euo pipefail

log_file="/home/yang/go-projects/stratum/.codex/hooks/notify-stop.log"
printf '%s Codex Stop hook invoked\n' "$(date -Is)" >>"${log_file}"

powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -Command \
  "(New-Object -ComObject WScript.Shell).Popup('Codex 已完成任务', 5, 'Codex', 64) | Out-Null"

# MCP observation rollout runbook

This procedure rolls out MCP Governor observation without exposing credentials, sharing sessions, rewriting live configuration automatically, or disabling MCP. Perform every gate in order. Stop at the first failed gate.

## Ten rollout gates

1. Take a fresh governor snapshot and record the swap and PSI baseline before changing any client configuration. Check `mcp-governor snapshot --config ~/.config/mcp-governor/config.json`, `free -h`, `/proc/swaps`, and `/proc/pressure/memory`; do not display command arguments or credentials.
2. Back up each live client configuration separately with mode `0600`. Record the Codex, Claude Code, VSCode, and Lingma backup paths and checksums without printing their contents.
3. Render every candidate configuration into a new private temporary directory (mode `0700`), with candidate files mode `0600`; never render directly over a live config.
4. Validate native syntax and inspect a redacted diff for each candidate. Confirm the diff contains no credential, token, API key, URL credential, or secret value before installation.
5. Roll out one client at a time, in this order: Codex, Claude Code, VSCode, Lingma. Do not proceed to the next client until the current client passes its checks.
6. In each client, invoke a read-only tool on every enabled server and verify the corresponding event contains metadata only—client, service, tool, hashed session/repository identity, outcome, timing, size, and concurrency—with no request parameters, arguments, result body, error text, URL, or credential.
7. Observe the client for 30 minutes. Check initialization, tool availability, process/service health, event privacy, memory, swap, and PSI throughout the window.
8. If initialization fails, a cross-session result appears, or tools are missing, restore only that client's backup and restart only that client. Recheck it before resuming; do not roll back clients that already passed.
9. Start the seven-day observation window only after all four clients pass gates 5–8.
10. Generate and review the seven-day report. MCP Governor must never automatically disable, stop, restart, or rewrite an MCP service; any follow-up change requires a separate human decision.

## Preparation and backups

Confirm the installed executable and user services without exposing configuration contents:

```sh
command -v mcp-governor
systemctl --user status mcp-governor-observe.timer mcp-governor-report.timer
systemctl --user list-timers 'mcp-governor-*'
journalctl --user-unit=mcp-governor-observe.service --since today
journalctl --user-unit=mcp-governor-report.service --since today
```

Create a private backup directory, copy each actual live configuration to a distinct filename, then enforce `0600`. Substitute the locally verified client paths; do not use commands that print file contents.

```sh
backup_dir=$(mktemp -d)
chmod 0700 "$backup_dir"
cp -- "$CODEX_CONFIG" "$backup_dir/codex.config"
cp -- "$CLAUDE_CONFIG" "$backup_dir/claude.config"
cp -- "$VSCODE_CONFIG" "$backup_dir/vscode.config"
cp -- "$LINGMA_CONFIG" "$backup_dir/lingma.config"
chmod 0600 "$backup_dir"/*
sha256sum "$backup_dir"/*
```

Keep the path variables and checksum output private. Never display or paste the configuration bytes.

## Private rendering and validation

Render into a disposable directory, one client at a time:

```sh
candidate_dir=$(mktemp -d)
chmod 0700 "$candidate_dir"
mcp-governor render-config --config ~/.config/mcp-governor/config.json --client codex --governor "$(command -v mcp-governor)" --output "$candidate_dir/codex.toml"
mcp-governor render-config --config ~/.config/mcp-governor/config.json --client claude --governor "$(command -v mcp-governor)" --output "$candidate_dir/claude.json"
mcp-governor render-config --config ~/.config/mcp-governor/config.json --client vscode --governor "$(command -v mcp-governor)" --output "$candidate_dir/vscode.json"
mcp-governor render-config --config ~/.config/mcp-governor/config.json --client lingma --governor "$(command -v mcp-governor)" --output "$candidate_dir/lingma.json"
python3 -c 'import sys,tomllib; tomllib.load(open(sys.argv[1], "rb"))' "$candidate_dir/codex.toml"
python3 -m json.tool "$candidate_dir/claude.json" >/dev/null
python3 -m json.tool "$candidate_dir/vscode.json" >/dev/null
python3 -m json.tool "$candidate_dir/lingma.json" >/dev/null
```

Use a credential-redacting review tool or inspect only structural keys when comparing candidates with live files. Do not run an unredacted `cat`, `diff`, `jq .`, or editor capture on a credential-bearing live configuration.

## Observation and privacy checks

During each 30-minute client gate, verify that only the selected client was restarted and that its wrapper/fake or real MCP child exits when the client session exits. Use exact PIDs obtained from the client launch or service manager; never use broad `pkill`, `killall`, or process-name matching.

```sh
systemctl --user status mcp-governor-observe.timer mcp-governor-report.timer
mcp-governor snapshot --config ~/.config/mcp-governor/config.json
free -h
cat /proc/pressure/memory
find ~/.local/state/mcp-governor -type d -printf '%m %p\n'
find ~/.local/state/mcp-governor -type f -printf '%m %p\n'
```

Inspect event structure without displaying values:

```sh
find ~/.local/state/mcp-governor/events -type f -name '*.jsonl' -print0 |
  xargs -0 jq -r 'keys_unsorted | sort | join(",")' | sort -u
```

Expected tool-event keys are metadata fields only. If `params`, `arguments`, `result`, `error` text, URLs, request bodies, or credential-like fields appear, stop the rollout, preserve the affected files privately for investigation, and restore only the current client's backup.

## Rollback

Close the affected client, verify its exact wrapper/child PIDs have exited, restore only its matching `0600` backup with an atomic same-filesystem replacement, and restart only that client. Revalidate native syntax and repeat its read-only tool checks. Do not restore a backup belonging to another client and do not remove observation data needed for the incident review.

If a governor timer itself is unhealthy, stop the rollout and inspect the user-unit status and journal. Disabling a timer is a manual operator action, never a report outcome or automatic governor response.

## Seven-day report and cleanup

After all clients pass, keep the observation and report timers enabled for seven complete local calendar days. Generate the durable report and inspect the client/service/tool split:

```sh
mcp-governor report-latest --config ~/.config/mcp-governor/config.json
find ~/.local/state/mcp-governor/reports -type f -name 'report-*.json' -printf '%m %p\n'
jq '{start,end,tools:[.tools[]|{client,service,tool,calls,effective_hits,distinct_sessions,success_rate}],services}' \
  ~/.local/state/mcp-governor/reports/report-*.json
```

The report contains aggregates, not session hashes or payloads. Archive the approved report according to local policy. Remove the private candidate directory after rollout and remove backups only after the rollback-retention decision; use their exact recorded paths. Confirm no temporary wrapper, fake-server, or build process remains by checking the explicit PIDs captured during the work.

For a disposable four-client subprocess/config proof that never touches live client files, run:

```sh
./tools/mcp-governor/scripts/e2e_observation_test.sh
```

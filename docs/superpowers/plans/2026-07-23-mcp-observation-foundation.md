# MCP Observation Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `mcp-governor` with a privacy-safe service catalog, stdio observation proxy, four-client configuration rendering, and seven-day usage/resource reports without changing MCP behavior or disabling any server.

**Architecture:** Keep the existing process snapshot command compatible and add a thin per-client stdio proxy that forwards newline-delimited MCP JSON-RPC unchanged while retaining only request correlation in memory. Each proxy writes metadata-only JSONL to a private per-session file, avoiding shared append locks; an offline report command aggregates tool calls with existing PSS/USS snapshots. A strict service catalog is the source for server classification, session policy, client availability, and generated Codex/Claude Code/VS Code/Lingma wrapper configuration.

**Tech Stack:** Go 1.25, MCP JSON-RPC over stdio, strict JSON decoding, `os/exec`, systemd user units, Bash installer tests, existing `tools/mcp-governor` module.

---

## Scope And Follow-Up Plans

This plan implements the observation and configuration foundation only. It does
not start shared HTTP services. Execute later plans in this order after a valid
baseline exists:

1. `code-review-graph` per-repository Streamable HTTP sharing.
2. Playwright service sharing with per-session BrowserContext isolation.
3. Shared heavy backends for Chrome/CDP, claude-mem, and codebase-memory.
4. Compatibility experiments for Obsidian, Yinxiang, Fetch, and Memory stdio gateways.

No MCP is removed or disabled by this plan.

## Required Execution Skills And Guards

- Before changing code, run `bash scripts/quality/risk-regression-guard.sh --explain`.
- Use `superpowers:test-driven-development` for every implementation task.
- Because this changes MCP lifecycle and real client integration, use
  `stratum-e2e-development` for Task 10.
- Before completion, use `service-governance-audit`,
  `superpowers:requesting-code-review`, and
  `superpowers:verification-before-completion`.
- Work only in the isolated feature worktree; do not modify or commit on `main`.

## File Structure

### Existing files to modify

- `tools/mcp-governor/cmd/mcp-governor/main.go`: dispatch `snapshot`, `proxy`,
  `report`, and `render-config` commands without changing snapshot semantics.
- `tools/mcp-governor/cmd/mcp-governor/main_test.go`: command-level contracts.
- `tools/mcp-governor/internal/config/config.go`: version-2 catalog and observation paths.
- `tools/mcp-governor/internal/config/config_test.go`: strict catalog validation.
- `tools/mcp-governor/config.example.json`: complete local MCP catalog without credentials.
- `tools/mcp-governor/scripts/install-user-units.sh`: install private directories,
  salt, and report units without overwriting user configuration.
- `tools/mcp-governor/scripts/install_user_units_test.sh`: installer contract.
- `tools/mcp-governor/README.md`: operation, privacy, rollout, and rollback instructions.

### New files

- `tools/mcp-governor/internal/identity/hash.go`: HMAC-based local identifiers.
- `tools/mcp-governor/internal/identity/hash_test.go`: deterministic and privacy tests.
- `tools/mcp-governor/internal/observe/model.go`: metadata-only event schema.
- `tools/mcp-governor/internal/observe/tracker.go`: in-memory JSON-RPC request correlation.
- `tools/mcp-governor/internal/observe/tracker_test.go`: effective-hit, failure,
  cancellation, timeout, and secret non-retention tests.
- `tools/mcp-governor/internal/observe/writer.go`: private per-session JSONL writer.
- `tools/mcp-governor/internal/observe/writer_test.go`: permissions and atomic-line tests.
- `tools/mcp-governor/internal/proxy/stdio.go`: transparent child stdio proxy.
- `tools/mcp-governor/internal/proxy/stdio_test.go`: byte forwarding and lifecycle tests.
- `tools/mcp-governor/internal/report/aggregate.go`: seven-day aggregation.
- `tools/mcp-governor/internal/report/aggregate_test.go`: deterministic report fixtures.
- `tools/mcp-governor/internal/clientconfig/render.go`: four client renderers.
- `tools/mcp-governor/internal/clientconfig/render_test.go`: secret-free wrapper output contracts.
- `tools/mcp-governor/systemd/mcp-governor-report.service`: one-shot report generation.
- `tools/mcp-governor/systemd/mcp-governor-report.timer`: daily aggregation.
- `tools/mcp-governor/testdata/e2e/fake-mcp-server.go`: deterministic stdio server.
- `tools/mcp-governor/testdata/catalog.json`: multi-client fixture catalog.
- `docs/operations/mcp-observation-runbook.md`: seven-day operational runbook.

## Task 1: Introduce The Strict Version-2 Service Catalog

**Files:**

- Modify: `tools/mcp-governor/internal/config/config.go`
- Modify: `tools/mcp-governor/internal/config/config_test.go`
- Modify: `tools/mcp-governor/config.example.json`
- Test: `tools/mcp-governor/internal/config/example_classifier_test.go`

- [ ] **Step 1: Write failing decode and validation tests**

Add a version-2 fixture and tests covering transport, scope, session policy,
client availability, event paths, retention, and duplicate validation:

```go
const validV2Config = `{
  "version": 2,
  "output_path": "%h/state/snapshot.json",
  "registry_path": "%h/state/registry.json",
  "observation": {
    "events_dir": "%h/state/events",
    "reports_dir": "%h/state/reports",
    "salt_path": "%h/config/identity-salt",
    "raw_retention_days": 14
  },
  "services": [{
    "name": "code-review-graph",
    "all_args_contain": ["code-review-graph", "serve"],
    "command": "uvx",
    "args": ["code-review-graph", "serve", "--repo", "/home/yang/go-projects/stratum", "--auto-watch"],
    "transport": "stdio",
    "scope": "repository",
    "session_policy": "isolated",
    "clients": ["codex", "claude", "vscode", "lingma"]
  }]
}`

func TestDecodeVersion2Catalog(t *testing.T) {
    cfg, err := Decode(strings.NewReader(validV2Config))
    if err != nil {
        t.Fatalf("Decode: %v", err)
    }
    if cfg.Version != 2 || cfg.Observation.RawRetentionDays != 14 {
        t.Fatalf("unexpected config: %#v", cfg)
    }
    service := cfg.Services[0]
    if service.Transport != TransportStdio || service.Scope != ScopeRepository ||
        service.SessionPolicy != SessionIsolated || len(service.Clients) != 4 {
        t.Fatalf("unexpected service: %#v", service)
    }
}
```

Add table cases rejecting unknown enum values, duplicate clients, retention
outside 7–90 days, empty paths, and `scope=repository` without an isolated
session policy.

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd tools/mcp-governor
go test ./internal/config -run 'Version2|Observation|Transport|Scope|Session|Clients' -count=1
```

Expected: FAIL because the version-2 fields and enum types do not exist.

- [ ] **Step 3: Implement the version-2 types and validation**

Add these types while keeping version 1 readable for existing installations:

```go
type Transport string
type Scope string
type SessionPolicy string
type Client string

const (
    TransportStdio          Transport = "stdio"
    TransportStreamableHTTP Transport = "streamable_http"
    TransportSSE            Transport = "sse"

    ScopeUser       Scope = "user"
    ScopeRepository Scope = "repository"
    ScopeSession    Scope = "session"

    SessionIsolated SessionPolicy = "isolated"
    SessionLocal    SessionPolicy = "session_local"

    ClientCodex  Client = "codex"
    ClientClaude Client = "claude"
    ClientVSCode Client = "vscode"
    ClientLingma Client = "lingma"
)

type Observation struct {
    EventsDir        string `json:"events_dir"`
    ReportsDir       string `json:"reports_dir"`
    SaltPath         string `json:"salt_path"`
    RawRetentionDays int    `json:"raw_retention_days"`
}

type ServiceRule struct {
    Name           string        `json:"name"`
    AllArgsContain []string      `json:"all_args_contain"`
    Command        string        `json:"command,omitempty"`
    Args           []string      `json:"args,omitempty"`
    Cwd            string        `json:"cwd,omitempty"`
    Transport      Transport     `json:"transport,omitempty"`
    Scope          Scope         `json:"scope,omitempty"`
    SessionPolicy  SessionPolicy `json:"session_policy,omitempty"`
    Clients        []Client      `json:"clients,omitempty"`
}
```

For version 1, assign compatibility defaults after decode: stdio, user scope,
isolated sessions, and all four clients. Do not silently default missing fields
in version 2. Version 2 requires a non-empty command. Reject command arguments
containing inline credential assignments matching `token=`, `password=`,
`secret=`, or `api-key=` case-insensitively; credentials must remain in the
client-owned environment.

- [ ] **Step 4: Update the example catalog**

Include every currently configured service without commands, arguments,
environment values, or credentials. Use classification fragments only:

```json
{
  "name": "mcp-delve",
  "all_args_contain": ["mcp-delve", "server.js"],
  "command": "node",
  "args": ["/home/yang/.claude/mcp-servers/mcp-delve/src/server.js"],
  "transport": "stdio",
  "scope": "session",
  "session_policy": "session_local",
  "clients": ["codex", "claude", "vscode", "lingma"]
}
```

Include code-review-graph, codegraph, codebase-memory, playwright,
chrome-devtools, obsidian, yinxiang, mcp-search, fetch, memory,
sequential-thinking, mcp-delve, tokensave, context7, and figma.

- [ ] **Step 5: Run all config tests**

Run:

```bash
cd tools/mcp-governor
go test ./internal/config ./internal/process -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the catalog contract**

```bash
git add tools/mcp-governor/internal/config tools/mcp-governor/config.example.json
git commit -m "[feat](mcp): define observation service catalog"
```

## Task 2: Add Privacy-Safe Local Identity Hashing

**Files:**

- Create: `tools/mcp-governor/internal/identity/hash.go`
- Create: `tools/mcp-governor/internal/identity/hash_test.go`

- [ ] **Step 1: Write failing identity tests**

```go
func TestHasherIsDeterministicAndDomainSeparated(t *testing.T) {
    hasher, err := NewHasher(bytes.Repeat([]byte{0x42}, SaltSize))
    if err != nil {
        t.Fatal(err)
    }
    first := hasher.Hash("session", "123:456")
    if first != hasher.Hash("session", "123:456") {
        t.Fatal("hash is not deterministic")
    }
    if first == hasher.Hash("repository", "123:456") {
        t.Fatal("hash domains are not separated")
    }
    if strings.Contains(first, "123") || len(first) != 32 {
        t.Fatalf("unsafe identifier %q", first)
    }
}

func TestLoadSaltRejectsWrongModeAndLength(t *testing.T) {
    // Write a short file and a 0644 file; both must fail closed.
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `cd tools/mcp-governor && go test ./internal/identity -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement HMAC-SHA256 identifiers**

```go
const SaltSize = 32

type Hasher struct{ key []byte }

func NewHasher(key []byte) (*Hasher, error) {
    if len(key) != SaltSize {
        return nil, fmt.Errorf("identity salt must be %d bytes", SaltSize)
    }
    return &Hasher{key: bytes.Clone(key)}, nil
}

func (h *Hasher) Hash(domain, value string) string {
    mac := hmac.New(sha256.New, h.key)
    _, _ = io.WriteString(mac, domain)
    _, _ = mac.Write([]byte{0})
    _, _ = io.WriteString(mac, value)
    return hex.EncodeToString(mac.Sum(nil))[:32]
}
```

`LoadSalt` must use `os.Lstat`, reject symlinks, require a regular `0600` file,
and require exactly 32 bytes. It must never create or weaken the salt file.

- [ ] **Step 4: Run identity tests**

Run: `cd tools/mcp-governor && go test ./internal/identity -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/mcp-governor/internal/identity
git commit -m "[feat](mcp): add private observation identifiers"
```

## Task 3: Define Metadata-Only Events And JSON-RPC Tracking

**Files:**

- Create: `tools/mcp-governor/internal/observe/model.go`
- Create: `tools/mcp-governor/internal/observe/tracker.go`
- Create: `tools/mcp-governor/internal/observe/tracker_test.go`

- [ ] **Step 1: Write failing tracker tests**

Use raw messages containing unique secrets and assert they never appear in the
event JSON:

```go
func TestTrackerRecordsSuccessfulEffectiveToolCallWithoutPayload(t *testing.T) {
    tracker := NewTracker(fixedClock, Metadata{
        Client: "codex", Service: "obsidian",
        SessionHash: "session-hash", RepositoryHash: "repo-hash",
    })
    request := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"read_note","arguments":{"path":"SECRET-NOTE"}}}`)
    tracker.ClientMessage(request)
    response := []byte(`{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"SECRET-BODY"}]}}`)
    events := tracker.ServerMessage(response)
    if len(events) != 1 || events[0].Tool != "read_note" ||
        events[0].Outcome != OutcomeSuccess || !events[0].Effective {
        t.Fatalf("unexpected events: %#v", events)
    }
    data, _ := json.Marshal(events[0])
    if bytes.Contains(data, []byte("SECRET")) {
        t.Fatalf("event leaked content: %s", data)
    }
}
```

Add tests for JSON-RPC string and numeric IDs, error responses, timeout-shaped
errors, cancellation notifications, empty results, help-only results, malformed
messages, duplicate IDs, initialization latency, and
`Flush(OutcomeDisconnected)`.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `cd tools/mcp-governor && go test ./internal/observe -run Tracker -count=1`

Expected: FAIL because the observe package does not exist.

- [ ] **Step 3: Implement the event schema**

```go
const EventVersion = 1

type Outcome string
type Kind string

const (
    KindToolCall     Kind = "tool_call"
    KindSessionReady Kind = "session_ready"

    OutcomeSuccess      Outcome = "success"
    OutcomeError        Outcome = "error"
    OutcomeTimeout      Outcome = "timeout"
    OutcomeCancelled    Outcome = "cancelled"
    OutcomeDisconnected Outcome = "disconnected"
)

type Event struct {
    Version        int       `json:"version"`
    Kind           Kind      `json:"kind"`
    At             time.Time `json:"at"`
    Client         string    `json:"client"`
    Service        string    `json:"service"`
    Tool           string    `json:"tool"`
    SessionHash    string    `json:"session_hash"`
    RepositoryHash string    `json:"repository_hash,omitempty"`
    Outcome        Outcome   `json:"outcome"`
    Effective      bool      `json:"effective"`
    DurationMS     int64     `json:"duration_ms"`
    ResponseBytes  int       `json:"response_bytes"`
    ConcurrentCalls int      `json:"concurrent_calls,omitempty"`
}
```

Do not add request parameters, response bodies, URLs, process arguments,
environment variables, prompts, or error text.

- [ ] **Step 4: Implement in-memory request correlation**

Store only normalized request ID, tool name, and start time:

```go
type pendingCall struct {
    tool      string
    startedAt time.Time
    concurrency int
}

type Tracker struct {
    now      func() time.Time
    metadata Metadata
    pending  map[string]pendingCall
}
```

`tools/call` reads only `params.name`. Record the current pending-call count in
the in-memory entry so the completion event exposes concurrency without
persisting a separate request event. `notifications/cancelled` removes and
emits only the matching pending call. A response with `error` is unsuccessful.
Classify an error as timeout only when its in-memory error code or normalized
message contains `timeout` or `deadline`; do not retain that text. Track the
MCP initialize request/response in memory and emit `kind=session_ready` with
startup duration but no method payload.
An effective result requires non-empty MCP content and excludes a content item
whose normalized text starts with `usage:`, `available tools:`, or `help:`.
Malformed or unrelated messages pass through and emit no event.

- [ ] **Step 5: Run tracker tests**

Run: `cd tools/mcp-governor && go test ./internal/observe -run Tracker -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tools/mcp-governor/internal/observe/model.go \
  tools/mcp-governor/internal/observe/tracker.go \
  tools/mcp-governor/internal/observe/tracker_test.go
git commit -m "[feat](mcp): track metadata-only tool calls"
```

## Task 4: Write Private Per-Session JSONL Event Files

**Files:**

- Create: `tools/mcp-governor/internal/observe/writer.go`
- Create: `tools/mcp-governor/internal/observe/writer_test.go`

- [ ] **Step 1: Write failing writer tests**

```go
func TestWriterCreatesPrivateSessionFileAndOneLinePerEvent(t *testing.T) {
    root := t.TempDir()
    writer, err := NewWriter(root, "codex", "session-hash")
    if err != nil {
        t.Fatal(err)
    }
    event := Event{
        Version: 1, Kind: KindToolCall, At: fixedTime,
        Client: "codex", Service: "fetch", Tool: "fetch",
        SessionHash: "session-hash", Outcome: OutcomeSuccess,
    }
    if err := writer.Write(event); err != nil {
        t.Fatal(err)
    }
    if err := writer.Close(); err != nil {
        t.Fatal(err)
    }
    assertMode(t, root, 0o700)
    path := filepath.Join(root, "codex", "session-hash.jsonl")
    assertMode(t, filepath.Dir(path), 0o700)
    assertMode(t, path, 0o600)
    if got := bytes.Count(mustRead(t, path), []byte{'\n'}); got != 1 {
        t.Fatalf("newline count = %d", got)
    }
}
```

Add tests rejecting unsafe client/session path components, symlink targets,
wrong existing modes, invalid events, writes after close, and concurrent writers
using different session files.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `cd tools/mcp-governor && go test ./internal/observe -run Writer -count=1`

Expected: FAIL because `NewWriter` does not exist.

- [ ] **Step 3: Implement the writer**

Create `events/<client>/<session-hash>.jsonl` with `O_CREATE|O_APPEND|O_WRONLY`
and mode `0600`. Validate existing directory and file modes without chmodding
user-owned paths. Marshal to a temporary byte slice, append one newline, and
perform one `Write` call per event.

```go
func (w *Writer) Write(event Event) error {
    if err := event.Validate(); err != nil {
        return err
    }
    data, err := json.Marshal(event)
    if err != nil {
        return fmt.Errorf("encode event: %w", err)
    }
    data = append(data, '\n')
    if _, err := w.file.Write(data); err != nil {
        return fmt.Errorf("append event: %w", err)
    }
    return nil
}
```

- [ ] **Step 4: Run writer tests**

Run: `cd tools/mcp-governor && go test ./internal/observe -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/mcp-governor/internal/observe/writer.go \
  tools/mcp-governor/internal/observe/writer_test.go
git commit -m "[feat](mcp): persist private observation events"
```

## Task 5: Build The Transparent Stdio Observation Proxy

**Files:**

- Create: `tools/mcp-governor/internal/proxy/stdio.go`
- Create: `tools/mcp-governor/internal/proxy/stdio_test.go`
- Create: `tools/mcp-governor/testdata/e2e/fake-mcp-server.go`

- [ ] **Step 1: Write failing byte-forwarding and lifecycle tests**

Use in-memory pipes for unit tests and the fake server for the subprocess test:

```go
func TestRunForwardsMessagesUnchangedAndRecordsToolCall(t *testing.T) {
    clientRequest := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"secret":"DO-NOT-LOG"}}}` + "\n")
    serverResponse := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}` + "\n")
    // Fake child reads clientRequest and writes serverResponse.
    // Assert exact bytes in both directions and one metadata-only event.
}
```

Add tests for partial reads, multiple lines in one read, a final line without a
newline, oversized lines above 64 KiB with a configured 8 MiB maximum, client
EOF, child exit, stderr passthrough, signal cancellation, and pending-call
flush on disconnect.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `cd tools/mcp-governor && go test ./internal/proxy -count=1`

Expected: FAIL because the proxy package does not exist.

- [ ] **Step 3: Implement line-preserving bidirectional forwarding**

Define explicit dependencies:

```go
type Options struct {
    Command string
    Args    []string
    Env     []string
    Dir     string
    Stdin   io.Reader
    Stdout  io.Writer
    Stderr  io.Writer
    Tracker *observe.Tracker
    Events  interface{ Write(observe.Event) error }
}
```

Start the child with a derived cancellable context. Forward client-to-child and
child-to-client concurrently, track every complete JSON-RPC line in memory,
and write emitted events. The copied bytes must be identical to input bytes.
On the first terminal error: cancel, close pipes, wait for both copy goroutines,
wait for the child, flush pending calls, then return the primary error plus any
cleanup error using `errors.Join`.

Use `bufio.Reader.ReadString('\n')`, not `Scanner`, so valid messages are not
silently truncated. Reject an individual message above 8 MiB with a stable
error and terminate the child.

- [ ] **Step 4: Implement the deterministic fake server**

The fake server must support `echo`, `error`, `sleep`, and cancellation without
network access. It must write protocol messages only to stdout and diagnostics
only to stderr.

- [ ] **Step 5: Run proxy tests with the race detector**

Run:

```bash
cd tools/mcp-governor
go test -race ./internal/proxy -count=1
```

Expected: PASS with no race reports or goroutine leaks.

- [ ] **Step 6: Commit**

```bash
git add tools/mcp-governor/internal/proxy tools/mcp-governor/testdata/e2e
git commit -m "[feat](mcp): proxy stdio with call observation"
```

## Task 6: Add The `proxy` CLI Without Breaking `snapshot`

**Files:**

- Modify: `tools/mcp-governor/cmd/mcp-governor/main.go`
- Modify: `tools/mcp-governor/cmd/mcp-governor/main_test.go`

- [ ] **Step 1: Write failing CLI tests**

```go
func TestParseProxyArgsRequiresCommandSeparator(t *testing.T) {
    _, err := parseProxyArgs([]string{
        "--config", "config.json", "--client", "codex",
        "--service", "fetch", "--session", "123:456",
    })
    if err == nil || !strings.Contains(err.Error(), "-- command") {
        t.Fatalf("unexpected error: %v", err)
    }
}

func TestProxyDoesNotPersistCommandArguments(t *testing.T) {
    // Run a fake server with a secret argument and assert no event/config output contains it.
}
```

Also retain the existing snapshot tests unchanged and add rejection tests for
unknown clients/services, empty session identity, repository scope without a
repository, and a service not enabled for the selected client.

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd tools/mcp-governor
go test ./cmd/mcp-governor -run 'Proxy|Snapshot' -count=1
```

Expected: FAIL for new proxy tests while existing snapshot tests remain PASS.

- [ ] **Step 3: Refactor command dispatch**

Keep `snapshot` parsing in its current behavior and dispatch explicitly:

```go
func run(args []string, stdout, stderr io.Writer) int {
    if len(args) == 0 {
        printUsage(stderr)
        return 2
    }
    switch args[0] {
    case "snapshot":
        return runSnapshot(args[1:], stdout, stderr)
    case "proxy":
        return runProxy(args[1:], os.Stdin, stdout, stderr)
    case "report":
        return runReport(args[1:], stdout, stderr)
    case "render-config":
        return runRenderConfig(args[1:], stdout, stderr)
    default:
        fmt.Fprintf(stderr, "unknown command %q\n", args[0])
        printUsage(stderr)
        return 2
    }
}
```

`proxy` syntax:

```text
mcp-governor proxy --config PATH --client CLIENT --service SERVICE \
  [--session PID:START_TICKS] [--repository PATH] -- COMMAND [ARG...]
```

Resolve the catalog, load the private salt, hash session and canonical
repository identity, create the event writer, and call `proxy.Run`. When
`--session` is absent, derive identity from the direct parent PID plus procfs
start ticks and fail closed if identity cannot be read. The explicit flag exists
only for deterministic tests. Pass the child's inherited environment without
reading or logging values.

- [ ] **Step 4: Run command and full module tests**

Run:

```bash
cd tools/mcp-governor
go test ./cmd/mcp-governor ./internal/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools/mcp-governor/cmd/mcp-governor
git commit -m "[feat](mcp): expose observation proxy command"
```

## Task 7: Generate A Deterministic Seven-Day Report

**Files:**

- Create: `tools/mcp-governor/internal/report/aggregate.go`
- Create: `tools/mcp-governor/internal/report/aggregate_test.go`
- Modify: `tools/mcp-governor/cmd/mcp-governor/main.go`
- Modify: `tools/mcp-governor/cmd/mcp-governor/main_test.go`

- [ ] **Step 1: Write failing aggregation tests**

```go
func TestAggregateExcludesHealthChecksAndComputesPercentiles(t *testing.T) {
    events := []observe.Event{
        event("codex", "codegraph", "impact", observe.OutcomeSuccess, true, 10, 100),
        event("codex", "codegraph", "impact", observe.OutcomeSuccess, true, 30, 200),
        event("claude", "codegraph", "impact", observe.OutcomeError, false, 50, 80),
    }
    got := Aggregate(events, nil, Window{Start: day1, End: day8})
    row := got.Tools[0]
    if row.Calls != 3 || row.EffectiveHits != 2 || row.SuccessRate != 2.0/3.0 ||
        row.P50DurationMS != 30 || row.P95DurationMS != 50 {
        t.Fatalf("unexpected row: %#v", row)
    }
}
```

Add fixtures for active days, distinct sessions, client split, cancellations,
timeouts, response-size percentiles, maximum concurrency, session-ready cold
start latency, unique process identities from repeated snapshots, PSS/USS
maxima from snapshots, deterministic sorting, malformed JSONL, stale
events outside the window, and insufficient coverage below seven days.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `cd tools/mcp-governor && go test ./internal/report -count=1`

Expected: FAIL because the report package does not exist.

- [ ] **Step 3: Implement strict event reading and aggregation**

Report types must contain only aggregate metadata:

```go
type ToolRow struct {
    Client            string  `json:"client"`
    Service           string  `json:"service"`
    Tool              string  `json:"tool"`
    Calls             int     `json:"calls"`
    EffectiveHits     int     `json:"effective_hits"`
    ActiveDays        int     `json:"active_days"`
    DistinctSessions  int     `json:"distinct_sessions"`
    SuccessRate       float64 `json:"success_rate"`
    P50DurationMS     int64   `json:"p50_duration_ms"`
    P95DurationMS     int64   `json:"p95_duration_ms"`
    P95ResponseBytes  int     `json:"p95_response_bytes"`
}

type ServiceRow struct {
    Service              string `json:"service"`
    ProcessStarts        int    `json:"process_starts"`
    PeakPSSBytes         uint64 `json:"peak_pss_bytes"`
    PeakUSSBytes         uint64 `json:"peak_uss_bytes"`
    MaxConcurrentCalls   int    `json:"max_concurrent_calls"`
    P50ColdStartMS       int64  `json:"p50_cold_start_ms"`
    P95ColdStartMS       int64  `json:"p95_cold_start_ms"`
}
```

Reject duplicate JSON keys and unknown event fields. A malformed event makes
the report command fail without replacing the previous report. Sort by service,
tool, then client. Percentiles use nearest-rank on sorted integer samples.
After a report is successfully and atomically installed, prune only regular,
non-symlink event files whose parsed event timestamps are wholly older than the
configured retention boundary. A prune error fails the command and remains
visible; never delete by filename timestamp alone.

- [ ] **Step 4: Add the CLI report command**

Syntax:

```text
mcp-governor report --config PATH --from 2026-07-23T00:00:00+08:00 \
  --to 2026-07-30T00:00:00+08:00 [--output PATH|-]
```

The default output is `<reports_dir>/report-<from>-<to>.json`. Use the existing
private atomic writer. Refuse a window shorter than seven complete days unless
`--allow-partial` is explicitly supplied for smoke tests.

- [ ] **Step 5: Run report and command tests**

Run:

```bash
cd tools/mcp-governor
go test ./internal/report ./cmd/mcp-governor -run 'Report|Aggregate|Snapshot' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tools/mcp-governor/internal/report tools/mcp-governor/cmd/mcp-governor
git commit -m "[feat](mcp): aggregate seven-day usage reports"
```

## Task 8: Render Secret-Free Wrapper Configuration For Four Clients

**Files:**

- Create: `tools/mcp-governor/internal/clientconfig/render.go`
- Create: `tools/mcp-governor/internal/clientconfig/render_test.go`
- Modify: `tools/mcp-governor/cmd/mcp-governor/main.go`
- Modify: `tools/mcp-governor/cmd/mcp-governor/main_test.go`
- Create: `tools/mcp-governor/testdata/catalog.json`

- [ ] **Step 1: Write failing renderer tests**

```go
func TestRenderCodexWrapsCommandWithoutEmbeddingEnvironment(t *testing.T) {
    catalog := fixtureCatalog(t)
    got, err := Render(Options{
        Client: ClientCodex,
        ConfigPath: "/home/test/.config/mcp-governor/config.json",
        GovernorPath: "/home/test/.local/bin/mcp-governor",
        Services: catalog.Services,
    })
    if err != nil {
        t.Fatal(err)
    }
    text := string(got)
    if !strings.Contains(text, `command = "/home/test/.local/bin/mcp-governor"`) ||
        !strings.Contains(text, `"proxy"`) || strings.Contains(text, "TOKEN") {
        t.Fatalf("unexpected output: %s", text)
    }
}
```

Add golden tests for Claude Code JSON, VS Code `mcp.json`, and Lingma JSON.
Assert stable ordering, valid syntax, no environment section, and no runtime
PID placeholders because the proxy derives its direct parent identity. Reject a
service unavailable to the selected client.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `cd tools/mcp-governor && go test ./internal/clientconfig -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement typed renderers**

Do not hand-build JSON. Use `encoding/json` for Claude, VS Code, and Lingma.
Use a small TOML encoder function for Codex that quotes every string with
`strconv.Quote` and emits only known fields.

Each rendered command wraps the original command conceptually as:

```text
mcp-governor proxy --config <catalog> --client <client> \
  --service <name> -- <catalog command and args...>
```

The service catalog contains no credential values. Existing client-specific
environment configuration remains outside rendered output and is inherited by
the launched proxy.

- [ ] **Step 4: Add `render-config` command**

Syntax:

```text
mcp-governor render-config --config PATH --client codex|claude|vscode|lingma \
  --governor PATH --output PATH|-
```

The command only renders; it never overwrites live client configuration.

- [ ] **Step 5: Run renderer and command tests**

Run:

```bash
cd tools/mcp-governor
go test ./internal/clientconfig ./cmd/mcp-governor -run 'Render|Proxy|Snapshot' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tools/mcp-governor/internal/clientconfig \
  tools/mcp-governor/testdata/catalog.json \
  tools/mcp-governor/cmd/mcp-governor
git commit -m "[feat](mcp): render observed client configs"
```

## Task 9: Install Observation State And Daily Report Units Safely

**Files:**

- Modify: `tools/mcp-governor/scripts/install-user-units.sh`
- Modify: `tools/mcp-governor/scripts/install_user_units_test.sh`
- Create: `tools/mcp-governor/systemd/mcp-governor-report.service`
- Create: `tools/mcp-governor/systemd/mcp-governor-report.timer`
- Modify: `tools/mcp-governor/README.md`

- [ ] **Step 1: Write failing installer tests**

Extend the fake-home installer test to assert:

```bash
test "$(stat -c '%a' "$fake_home/.config/mcp-governor/identity-salt")" = 600
test "$(stat -c '%s' "$fake_home/.config/mcp-governor/identity-salt")" = 32
test "$(stat -c '%a' "$fake_home/.local/state/mcp-governor/events")" = 700
test "$(stat -c '%a' "$fake_home/.local/state/mcp-governor/reports")" = 700
test -f "$fake_home/.config/systemd/user/mcp-governor-report.service"
test -f "$fake_home/.config/systemd/user/mcp-governor-report.timer"
```

Run the installer twice and assert the salt and existing catalog are byte-for-byte
unchanged. Assert systemctl enables both timers.

- [ ] **Step 2: Run the installer test and verify it fails**

Run:

```bash
bash tools/mcp-governor/scripts/install_user_units_test.sh
```

Expected: FAIL because the new directories, salt, and report units are absent.

- [ ] **Step 3: Implement safe installation**

Generate the salt only when absent:

```bash
salt_path="$config_dir/identity-salt"
if [[ ! -e "$salt_path" ]]; then
  umask 077
  head -c 32 /dev/urandom >"$salt_path"
  chmod 0600 "$salt_path"
fi
```

Before reuse, reject a symlink, a non-regular file, a size other than 32 bytes,
or permissions other than `0600`. Create events/reports directories with
`0700`. Install both report units and enable both timers. Do not modify an
existing catalog or salt.

The report service invokes a separate helper command that computes the previous
seven complete local days. Do not embed shell command substitution in the unit;
add `mcp-governor report-latest --config ...` if date calculation is needed.

- [ ] **Step 4: Add unit hardening consistent with WSL PSS visibility**

Retain the proven-compatible directives:

```ini
UMask=0077
NoNewPrivileges=yes
RestrictAddressFamilies=AF_UNIX
LockPersonality=yes
```

Do not add `PrivateTmp`, `ProtectSystem`, `ProtectHome`, or `ReadWritePaths` to
the snapshot unit because the repository baseline proved those directives hide
same-user `smaps_rollup` under this WSL/systemd combination.

- [ ] **Step 5: Run installer and module tests**

Run:

```bash
bash tools/mcp-governor/scripts/install_user_units_test.sh
cd tools/mcp-governor
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tools/mcp-governor/scripts tools/mcp-governor/systemd tools/mcp-governor/README.md
git commit -m "[feat](mcp): install observation report services"
```

## Task 10: Prove End-To-End Behavior Across Real Client Shapes

**Files:**

- Create: `tools/mcp-governor/scripts/e2e-observation.sh`
- Create: `tools/mcp-governor/scripts/e2e_observation_test.sh`
- Create: `docs/operations/mcp-observation-runbook.md`
- Modify: `tools/mcp-governor/README.md`

- [ ] **Step 1: Activate the required E2E skill and read same-domain tests**

Invoke `stratum-e2e-development`. Read
`tools/mcp-governor/scripts/install_user_units_test.sh` and
`tools/mcp-governor/cmd/mcp-governor/main_test.go` completely before creating
the E2E harness.

- [ ] **Step 2: Write a failing shell E2E test**

The test builds the governor and fake MCP server, launches four proxy processes
labelled Codex, Claude, VS Code, and Lingma, sends overlapping `tools/call`
requests, cancels one request, and closes one client abruptly.

Required assertions:

```bash
jq -e 'all(.[]; has("client") and has("service") and has("tool") and
  has("session_hash") and (has("params")|not) and (has("result")|not))' "$events_json"
jq -e '[.[] | .session_hash] | unique | length == 4' "$events_json"
jq -e '[.[] | select(.outcome == "cancelled")] | length == 1' "$events_json"
jq -e '[.[] | select(.effective == true)] | length >= 3' "$events_json"
! rg -n 'DO-NOT-LOG|SECRET-BODY|TOKEN=' "$state_dir"
```

Also assert all proxy and fake-server processes exit within 10 seconds and no
event file has permissions broader than `0600`.

- [ ] **Step 3: Run the E2E test and verify it fails**

Run: `bash tools/mcp-governor/scripts/e2e_observation_test.sh`

Expected: FAIL because the harness is absent.

- [ ] **Step 4: Implement the E2E harness**

Use `mktemp -d`, explicit PIDs, bounded polling, and a cleanup trap that sends
TERM, waits, then reports any process that did not exit. Never use broad
`pkill`, unresolved globs, or process-name matching.

The harness must not touch live client configuration. It renders fixtures into
the temporary directory and exercises the same command shapes that the four
clients will use.

- [ ] **Step 5: Document the seven-day rollout and rollback**

The runbook must contain these exact gates:

1. Capture a fresh governor snapshot and current swap/PSI baseline.
2. Back up each live client MCP configuration with mode `0600`.
3. Render candidate wrapper configuration to a temporary private directory.
4. Validate syntax and diff without credentials.
5. Migrate one client at a time: Codex, Claude Code, VS Code, then Lingma.
6. For each client, call one read-only tool from every enabled server and verify
   an event without payload content.
7. Observe 30 minutes before migrating the next client.
8. On initialization failure, cross-session behavior, or missing tools, restore
   only that client's backup and restart that client.
9. Start the seven-day window only after all four clients pass.
10. Generate the report; do not disable any MCP automatically.

- [ ] **Step 6: Run E2E and fast verification**

Run:

```bash
bash tools/mcp-governor/scripts/e2e_observation_test.sh
cd tools/mcp-governor
go vet ./...
go test -race ./... -count=1
```

Expected: all commands exit 0; the race test reports no races.

- [ ] **Step 7: Commit**

```bash
git add tools/mcp-governor/scripts/e2e-observation.sh \
  tools/mcp-governor/scripts/e2e_observation_test.sh \
  tools/mcp-governor/README.md \
  docs/operations/mcp-observation-runbook.md
git commit -m "[test](mcp): verify cross-client observation flow"
```

## Task 11: Perform Governance Review And Final Verification

**Files:**

- Modify only files required by confirmed review findings.

- [ ] **Step 1: Run the service-governance audit**

Invoke `service-governance-audit` and review the completed MCP governor changes
for timeout budgets, bounded restart, backpressure, child-process cleanup,
failure propagation, and admission behavior. Fix only findings confirmed by
code and tests.

- [ ] **Step 2: Run the repository risk guardrails**

Run:

```bash
make risk-guardrails
```

Expected: PASS. Any failure must remain visible and be fixed or reported; do
not bypass the guard.

- [ ] **Step 3: Run the complete targeted verification**

Run:

```bash
cd tools/mcp-governor
go vet ./...
go test -v -race -timeout 30s ./...
cd ../..
bash tools/mcp-governor/scripts/install_user_units_test.sh
bash tools/mcp-governor/scripts/e2e_observation_test.sh
git diff --check
```

Expected: all commands exit 0 with no races, timeouts, leaked secrets, or
working-tree whitespace errors.

- [ ] **Step 4: Request code review**

Invoke `superpowers:requesting-code-review`. Verify every reported issue before
changing code. If feedback is unclear or technically questionable, use
`superpowers:receiving-code-review` before applying it.

- [ ] **Step 5: Re-run verification after review changes**

Repeat the complete command block from Step 3. Expected: all commands exit 0.

- [ ] **Step 6: Commit review fixes, if any**

```bash
git add tools/mcp-governor docs/operations/mcp-observation-runbook.md
git commit -m "[fix](mcp): close observation review findings"
```

Skip the commit when review produces no code or documentation changes.

## Task 12: Start The Controlled Seven-Day Observation

**Files:**

- Runtime state only; do not commit generated client configs, events, salts,
  snapshots, reports, or credentials.

- [ ] **Step 1: Capture the pre-migration baseline**

Run the commands from `docs/operations/mcp-observation-runbook.md` to record:

- current process count by MCP service;
- PSS and USS by service;
- RAM, swap, and PSI;
- current client configuration hashes;
- start timestamp and installed tool versions.

Expected: a private baseline artifact with no process arguments or credentials.

- [ ] **Step 2: Render and validate all four client configurations**

Render into a `mktemp -d` directory with mode `0700`. Parse TOML/JSON and inspect
the diff. Expected: only MCP command wrappers change; environment and credential
configuration remains in the existing client-owned location.

- [ ] **Step 3: Migrate one client at a time**

Apply Codex, wait 30 minutes and verify; then Claude Code, VS Code, and Lingma.
At every gate, run one read-only call per enabled MCP and confirm a metadata-only
event. Stop and restore only the failing client's backup on any regression.

- [ ] **Step 4: Record the observation window**

Write the exact start and scheduled end timestamps to the private state
directory. Confirm both snapshot and report timers are active.

- [ ] **Step 5: Generate the seven-day report**

After seven complete days, run `mcp-governor report` for the recorded window.
Expected: the report contains tool/client/service aggregates, PSS/USS resource
data, coverage status, and no raw arguments or content.

- [ ] **Step 6: Stop before consolidation**

Present the report and recommendations to the user. Do not remove, disable, or
merge codegraph, code-review-graph, codebase-memory, or any other MCP without a
new explicit approval and follow-up design/plan.

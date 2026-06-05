# ClawHermes-AI-Go Rules

**Default**: Caution > speed. Correctness, clarity, safety over velocity.

## Karpathy 12 Rules

| # | Rule | Mandate |
|---|------|---------|
| 1 | Think Before Coding | State all assumptions. If ambiguous, list interpretations and ask. No silent guessing. |
| 2 | Simplicity First | Minimal correct solution only. No speculative abstractions, no premature optimization. |
| 3 | Surgical Changes | Modify only task-relevant code. No unrelated refactor/rename/style. Match project style. |
| 4 | Verify Before Done | Define success criteria first. No untested code submitted. |
| 5 | No AI Control Logic | AI does language tasks only. Routing/retry/state machines must be hard-coded. |
| 6 | Token Budget | Task ≤4k tokens, session ≤30k. At limit: summarize → checkpoint → reset → continue. Pause only at 28k (95%). |
| 7 | Resolve Conflicts | Pick one valid approach; remove the other. No hybrid compromise code. |
| 8 | Read Fully First | Read all related files/interfaces/call chains before writing. Nothing is "unrelated" by appearance. |
| 9 | Validate Business Intent | Tests verify business correctness, not just return values. |
| 10 | Checkpoints on Long Ops | Record done work + verified result + remaining tasks after each complex step. Stop on validation failure. |
| 11 | Project Convention | Follow existing architecture/patterns/style. No unauthorized pattern replacement. |
| 12 | Expose Errors | Declare all skips, uncertainties, partial failures. No silent failure tolerance. |

**Meta order**: Make it work → right → fast → scalable

## Layered Context

- Layer 2 (project facts): [`docs/agent/project.md`](docs/agent/project.md)
- Layer 3 (module rules): [milvus](docs/agent/milvus.md) · [nats](docs/agent/nats.md) · [api](docs/agent/api.md) · [agent](docs/agent/agent.md) · [observability](docs/agent/observability.md)

## Go Standards

**Style**: Go idioms · goroutine-safe · single responsibility · line ≤120 chars · imports: stdlib → third-party → internal · cyclomatic complexity ≤10 · all public symbols documented.

**Logging**: Zap only (no `fmt.Print`). Structured fields: `request_id / user_id / tenant_id / operation`. Never log passwords/tokens/PII.

**Error handling**: `fmt.Errorf("operation: %w", err)` — always wrap with context. Transient errors: exponential backoff (base 100ms, max 10s). External deps: circuit breaker pattern.

**Security**: Secrets in Vault/AWS Secrets Manager — never in git. TLS 1.2+ in transit, AES-256 at rest. Pre-commit: git-secrets/GitGuardian. Never modify `config/prod.yaml`.

**Testing**: Coverage ≥80% on logic paths. Table-driven tests. Mock all external deps. Race detector on full suite.

```bash
go vet && go test -short ./...          # after every change
go test -v -race -timeout 30s ./...     # full suite before PR
```

**PR format**: `[type](scope): description` — feat/fix/refactor/perf/test/docs/chore/ci. Include What/Why/HowToTest. CI (lint/test/scan) must pass.

---

## Frontend Standards (`web/`)

Stack: React 18 · Vite 4 · Ant Design 5 · React Router 6 · Axios

**Structure**: `components/` shared UI · `hooks/` custom hooks (`use*`) · `pages/` route components · `services/` API layer · `utils/` pure helpers · `contexts/` React Context. No cross-`pages/` imports.

**Components**: One component per file, PascalCase. Pages named `*Page.jsx`. Max 200 lines — extract to hooks/utils. No business logic in JSX. All user-visible strings in Chinese.

**State**: `useState` for local UI; Context for cross-component. No Redux/Zustand without approval. `useEffect` deps must be complete; async effects need cleanup (`let cancelled = false` pattern).

**API**: All calls via central axios instance in `services/api.js` — no raw `fetch`. Interceptor handles 401/403. Surface errors with `message.error(err.response?.data?.error || '操作失败')`. No `console.log` in committed code.

**Routing**: All routes in `App.jsx`. Protected routes use `<PrivateRoute>`. SPA refresh: Vite proxy uses `bypass` → `/index.html` for `text/html`; Nginx uses `try_files $uri /index.html`.

**Ant Design**: AntD components first. No `!important` overrides. Use `message`/`Modal.confirm` — no `alert()`/`confirm()`. Forms via `Form.Item` rules.

**Security**: No tokens in `localStorage` — use `httpOnly` cookies or in-memory Context. No secrets in `.env` committed to git.

**Validation**:

```bash
npm run lint   # zero warnings
npm run build  # must succeed before PR
```

**PR gate**: lint passes · build succeeds · new routes in `App.jsx` with `<PrivateRoute>` · no raw `fetch` · tested in browser including page refresh.

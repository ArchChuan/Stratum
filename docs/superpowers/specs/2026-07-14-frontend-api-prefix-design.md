# Frontend API Prefix Fix Design

## Problem

The deployed frontend sends `DELETE /api/memory/clear`. The frontend Nginx proxy already owns the `/api/` public prefix and strips it before forwarding requests to the backend. Frontend API modules therefore pass backend-relative paths such as `/agents` and `/tenant/settings` to the shared Axios client. The memory API is the only module that includes `/api` itself, so the backend receives an unmatched path and returns HTTP 404.

## Scope

- Change the memory clear request from `/api/memory/clear` to `/memory/clear`.
- Add a focused unit test that verifies the memory API calls the shared client with the backend-relative path.
- Add a source-level guard that scans frontend TypeScript request calls and rejects literal paths beginning with `/api/`.
- Audit Axios and fetch-based request construction in `web/src` for equivalent duplicated prefixes.

Backend routes, Nginx proxy behavior, and compatibility aliases are intentionally unchanged because they are already consistent for every other frontend API.

## Testing

The focused unit test must fail against the current implementation and pass after the path correction. The prefix guard must cover request calls made through the shared Axios client and direct `fetch` calls with literal paths. Final verification includes the relevant Vitest tests, frontend type checking, ESLint, and the production build.

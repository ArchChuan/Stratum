# IAM Token and Runtime Composition Boundaries

**Status:** Approved on 2026-07-20

## Goal

Finish the architecture-report remediation by moving cryptographic JWT details out of IAM application code and moving process composition out of `internal/platform` into the executable composition root.

## IAM token boundary

`internal/iam/domain/port` owns the consumer-facing token contract and plain claim values. `TokenClaims` preserves `sub`, `tid`, `role`, `global_role`, `system_role`, `jti`, `ava`, and `ghl`; `OnboardingClaims` preserves the current onboarding payload. The port exposes access-token and onboarding-token sign/verify operations with the existing TTL behavior.

`internal/iam/infrastructure/token` owns RSA keys, `golang-jwt`, registered claims, serialization, RS256 enforcement, and error wrapping. HTTP handlers, middleware, router assembly, and wiring depend only on the domain port. The HTTP contract and token wire format do not change.

## Runtime composition boundary

`cmd/server` owns process concerns: tracing initialization, tenant bootstrap sequencing, Harness registration, HTTP server lifecycle, signal handling, and shutdown. `internal/platform` retains reusable platform domain/application/infrastructure behavior but imports neither `api/http` nor `api/wiring`.

The existing component order and reverse shutdown behavior remain unchanged. Tenant bootstrap continues under one schema-provision lock in the order public schema, default tenant, then all tenant schemas. Tracing logs only that export is enabled; it does not log the configured endpoint.

## Verification

Architecture tests guard both boundaries. Existing JWT behavior tests move with the infrastructure implementation. Runtime tests move to `cmd/server`. Real PostgreSQL verification covers public and tenant provisioning, idempotency, tenant isolation, and invalid tenant rejection. A real server process must answer `/health`; an authenticated request is exercised when the existing route prerequisites can be established without widening scope.

`govulncheck` uses a one-shot WSL-to-Windows HTTP proxy environment only. No proxy configuration is persisted in Bash startup files.

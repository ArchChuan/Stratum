# Current-State Documentation Refresh Design

**Date:** 2026-07-21

**Source revision:** `37d8f05`

**Scope:** root `README.md` and current documentation under `docs/`

## Goal

Bring the project's active documentation back into alignment with the current code, tests, build tooling, and deployment configuration. The refresh must make the public deployment address unambiguous: external traffic uses `http://101.200.181.141:6879`, while the cluster forwards that traffic to the service's internal HTTP port.

## Evidence Order

Documentation claims will be resolved in this order:

1. Current source code, route registration, tests, and generated contracts.
2. Deployment manifests, scripts, CI workflows, `Makefile`, dependency manifests, and example configuration.
3. ADRs and active repository documentation.
4. Verified Obsidian notes for general engineering context only.
5. Official upstream documentation when a current external behavior or version claim cannot be established locally.

Repository behavior wins when sources disagree. Unverified or provisional notes will not be presented as project facts.

## Update Strategy

### README

Refresh the product overview, implemented capabilities, architecture summary, technology versions, development commands, deployment entry points, and document index. All public demo links, badges, and health-check examples must include port `6879`. Authentication features will be described according to actual runtime configuration requirements rather than as universally available.

### Active documents

Review and update documents that currently guide development or operation, including:

- top-level setup, configuration, persistence, LLM, deployment, and specification documents;
- `docs/agent/` architecture and module context;
- `docs/architecture/`, `docs/deployment/`, and `docs/operations/` current-state guides;
- generated package architecture documentation, using the repository generator when one exists.

Edits will be surgical: correct stale facts, consolidate duplicate guidance through links, and add an explicit source revision or applicability note where that prevents future ambiguity.

### Historical documents

Audit reports, completed plans, design proposals, and archived records retain their original historical conclusions. They will not be rewritten to look current. Only broken internal links or a short supersession note may be added when later runtime evidence makes continued use unsafe.

## Deployment Contract

The documentation will distinguish these layers:

| Layer | Address or port | Meaning |
|---|---:|---|
| Public entry | `http://101.200.181.141:6879` | Browser and external HTTP clients |
| Public health check | `http://101.200.181.141:6879/api/health` | Deployment reachability check |
| Traefik entrypoint | `6879` / `web2` | K3s edge listener |
| Internal service target | HTTP port `80` where configured | Cluster forwarding target, not the public URL |

No domain or HTTPS prerequisite will be documented for this deployment profile. Security guidance must still state that plain HTTP is a deployment-specific exception and must not imply TLS protection.

## Verification

The completed refresh will be checked with:

- repository-wide searches for stale public URLs, default-port assumptions, obsolete module names, and conflicting deployment instructions;
- validation of Markdown links and referenced local paths;
- comparison of documented versions with `go.mod`, frontend manifests, workflows, and deployment manifests;
- comparison of documented endpoints with router registration and API contract tests;
- the repository risk guardrails required for deployment and documentation changes;
- a final diff review that confirms historical records were not silently rewritten.

Live public reachability is runtime evidence, not a substitute for repository verification. If the public host is unavailable during validation, the documentation will record the expected contract without claiming the live service is healthy.

## Non-Goals

- No application code or runtime configuration changes.
- No modification of `config/prod.yaml`.
- No redesign of architecture or product behavior.
- No rewriting of historical audits or completed implementation plans as current specifications.
- No claim that OAuth, HTTPS, or domain-based access works unless current configuration and runtime evidence demonstrate it.

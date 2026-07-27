# Skill Publish State Closure Design

**Date:** 2026-07-27
**Status:** Approved
**Scope:** Skill workspace publish readiness and post-publish state

## Goal

Make the Skill workspace reflect the authoritative revision lifecycle during real use. An administrator must not be able to submit a publish request before confirming the activation contract, and a successfully published workspace must immediately become read-only without a manual refresh.

## Behavior

- Draft editing actions are available only when the loaded revision status is `draft`.
- An unconfirmed activation contract disables publishing and provides an action that opens the activation-contract tab.
- The backend remains authoritative and continues validating the complete publish contract.
- After a successful publish request, the frontend reloads the workspace instead of reconstructing Skill pointers locally.
- A workspace with no draft returns a dedicated conflict error from publish, not `skill not found`.
- Published workspaces keep their fields visible in a disabled, read-only form and do not expose save or publish actions.

## Error Contract

`ErrSkillDraftNotFound` represents an existing Skill with no publishable draft and maps to HTTP 409. `ErrSkillNotFound` remains reserved for a missing Skill. Validation failures such as an unconfirmed activation contract remain HTTP 400.

## Verification

- Frontend component tests cover the unconfirmed gate, repair navigation, successful refresh, and published read-only state.
- Application and middleware tests cover the dedicated no-draft conflict.
- Existing Skill tests, frontend lint/build, risk guardrails, and the real E2E/API path must remain green.

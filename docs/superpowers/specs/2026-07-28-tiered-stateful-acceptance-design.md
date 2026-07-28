# Tiered Stateful Acceptance Design

## Context

Stratum currently treats every soak attestation as release-grade and requires at least 3600 seconds. The active remote
environment is a test environment, so that duration makes normal development acceptance and PR integration too slow.
The browser, API, database, cleanup, and source-binding guarantees remain valuable and must not be weakened.

## Decision

Introduce two explicit acceptance profiles:

- `test`: minimum 600 seconds, used by the current remote test environment and PR acceptance.
- `release`: minimum 3600 seconds, reserved for an explicit production release workflow.

Both profiles apply only to soak mode. Short acceptance remains mode-only and carries no profile. A soak profile
changes only the minimum uninterrupted wall-clock duration; it does not reduce pack, capability, browser, evidence,
reconciliation, cleanup, freshness, or source-integrity requirements.

## Attestation Contract

Every soak attestation records its acceptance profile. Generation rejects a missing or unknown soak profile and
rejects a profile on short results. Soak verification requires an explicit profile and applies that profile's minimum
duration. A `test` attestation cannot satisfy `release`, even when its measured duration happens to exceed 3600
seconds; release evidence must be generated intentionally as release evidence. Short verification does not accept or
require a profile.

Existing attestations without a profile are invalid after this contract change. This is acceptable because
attestations are short-lived, source-bound evidence and the implementation change already invalidates their source
digest.

## CLI And Make Targets

The CLI generation and verification paths accept a required profile. Local targets expose deterministic defaults:

- `make e2e-system-soak` runs the `test` profile for 600 seconds.
- A separate release target or explicit profile invocation runs 3600 seconds.
- `make e2e-attestation-check` defaults to the `test` profile while allowing release automation to require `release`.

Duration overrides may increase a profile's duration but cannot lower it below the selected profile minimum.

## CI And Risk Classification

PR CI continues to classify whether a change requires `short` or `soak`. A classified soak requires the `test`
profile. CI only verifies the committed, source-bound attestation and does not run Chromium. Future production release
automation must explicitly require the `release` profile.

## Browser And Evidence Invariants

Both profiles retain all current system acceptance requirements:

- headless Chromium with isolated browser contexts;
- all 12 system packs and all manifest-required capabilities;
- primary operations performed through visible UI controls;
- positive UI, HTTP, database, and reconciliation evidence;
- deterministic seed and action-sequence digest;
- no failed, skipped, duplicate, or unverified capabilities;
- successful cleanup with no residual entity IDs;
- canonical, credential-free, unexpired artifacts bound to the current source and manifest digests.

## Failure Behavior

Unknown soak profiles, missing soak profile metadata, profile mismatch, insufficient duration, source or manifest mismatch,
missing evidence, cleanup residue, credential patterns, and artifact hash mismatch all fail closed. A short-mode
attestation cannot satisfy either soak profile.

## Verification

Tests cover profile parsing, the 600/3600 boundaries, cross-profile rejection, CLI flag propagation, Make/CI wiring,
skill instructions, and source-digest invalidation. Final PR evidence is one uninterrupted 600-second `test` soak
against frozen source followed by committed-HEAD verification.

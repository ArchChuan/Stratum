# Parallel Image Builds Design

## Goal

Reduce the `Build and Push` wall-clock time by building the backend, frontend, and Feishu alert adapter images in parallel while preserving the deployment job's existing input contract.

## Design

Replace the current serial `build-and-push` job with four jobs:

- `build-backend` builds and pushes the backend image.
- `build-frontend` builds and pushes the frontend image.
- `build-feishu-adapter` builds and pushes the Feishu alert adapter image and exports its registry digest.
- `build-and-push` is a lightweight fan-in job that waits for all three builds and exposes the existing `image-tag` and `adapter-digest` outputs.

All three image jobs continue to depend on `test`, so image publication starts only after tests pass. The existing `deploy` job continues to depend only on `build-and-push`; no deployment commands or image naming conventions change.

## Cache Isolation

Each Docker build uses its own GitHub Actions cache scope:

- backend: `backend`
- frontend: `frontend`
- Feishu alert adapter: `feishu-alert-adapter`

Both `cache-from` and `cache-to` use the same scope. This prevents the three serial exporters from replacing the shared default `buildkit` cache and allows unchanged dependency layers to survive subsequent workflow runs.

## Outputs And Failure Behavior

The fan-in job publishes `${{ github.sha }}` as `image-tag` and forwards the adapter digest from `build-feishu-adapter`. GitHub Actions dependency semantics provide fail-closed behavior: if any image build fails, the fan-in job and deployment are skipped.

## Verification

- Parse the workflow as YAML and inspect the resulting job dependency graph.
- Assert that all three image jobs depend on `test` and that none depends on another image job.
- Assert unique matching cache scopes for `cache-from` and `cache-to`.
- Assert the fan-in job depends on all image jobs and preserves the outputs consumed by `deploy`.
- Run repository risk guardrails before completion.

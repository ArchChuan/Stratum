# Verification Authorities

Stratum uses three independent authorities instead of one overloaded `accepted` status.

## Local browser authority

`make test-verify-before-pr` runs risk-selected headless browser verification on a clean commit. Its local report binds
the tested commit, verification manifest digest, attestation v2 capability results, and cleanup outcome. This report is
a developer audit assertion. GitHub does not download it, sign it, or treat it as a required status check.

## CI merge authority

GitHub Actions decides whether a PR may merge through real parallel jobs for static analysis, unit tests, integration
tests, contract goldens, security checks, and builds. Browser tools are absent from this workflow. Every
`ci_checks` identifier in `.test/verification.yaml` maps to a workflow job, and the compatibility aggregate fails unless
every required dependency result is `success`.

## Release pipeline authority

For a `workflow_run` deployment, the candidate is `github.event.workflow_run.head_sha`. It must be a successful CI run
for `main` and must still equal the current `main` tip before any image is built. All checkouts and image tags use this
candidate. Deployment pins registry digests and records the actual backend, frontend, and adapter image digests
observed from the cluster together with migration, health, and rollback results.

The release record may be attested by GitHub because it is produced inside the release control plane. That attestation
does not retroactively make local browser evidence a GitHub trust boundary.

# Verification CI Authority

The repository's `accepted` status is issued only after the completion report
verifies a Sigstore bundle itself. Verification pins the GitHub OIDC issuer,
repository, signer workflow, source commit, and source ref. Caller-provided CI
flags and `verified` JSON fields are not trust boundaries. Local evidence and
agent reviews remain diagnostic and produce `incomplete` reports.

Repository administrators must protect these GitHub environments with required
reviewers who are not implementation authors:

- `specification-review`
- `code-quality-review`
- `release-evidence-review`
- `production-verification`

The R3 workflow consumes commit-bound review receipts from the first two
environments and signs evidence only after those jobs complete. The R4 workflow
additionally requires release evidence approval, a 3600-second release soak,
and read-only production health verification. It accepts a successful Build and
Deploy run ID, verifies that workflow's signed deployment receipt, and obtains
immutable application image digests from the images actually recorded on the
cluster. It does not accept a manually entered artifact digest.

Environment protection and branch protection are external control-plane
prerequisites; a repository workflow cannot grant its own approval.

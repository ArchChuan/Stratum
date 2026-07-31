# Verification CI Authority

The repository's `accepted` status is issued only by GitHub Actions after a
Sigstore-backed artifact attestation verifies successfully. Local evidence and
agent reviews remain diagnostic and produce `incomplete` reports.

Repository administrators must protect these GitHub environments with required
reviewers who are not implementation authors:

- `specification-review`
- `code-quality-review`
- `release-evidence-review`
- `production-verification`

The R3 workflow consumes commit-bound review receipts from the first two
environments. The R4 workflow additionally requires release evidence approval,
a 3600-second release soak, an immutable OCI digest, and a read-only production
health verification. Environment protection and branch protection are external
control-plane prerequisites; a repository workflow cannot grant its own
approval.

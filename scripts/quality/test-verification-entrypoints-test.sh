#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
makefile=$root/Makefile

for target in plan local ci attestation report; do
  grep -Eq "^test-verify-${target}:" "$makefile" || {
    printf 'missing canonical verification target: test-verify-%s\n' "$target" >&2
    exit 1
  }
done
grep -Fq 'scripts/quality/test-verification-plan.sh' "$makefile"
grep -Fq 'scripts/quality/test-verification-report.sh' "$makefile"
grep -Fq 'scripts/quality/write-verification-check-receipt.sh' "$root/.github/workflows/stateful-e2e.yml"
grep -Eq '^test-verify-report:[[:space:]]*$' "$makefile"
grep -Fq 'e2e-attestation-check' "$makefile"
grep -Fq 'verification-schema' "$makefile"

printf 'canonical verification entrypoint contract passed\n'

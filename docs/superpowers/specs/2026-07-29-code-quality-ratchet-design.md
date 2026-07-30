# Code Quality Ratchet Design

## Goal

Add a repository-owned incremental quality guard that blocks new or worsened Go complexity without requiring an immediate rewrite of historical debt.

## Metrics

Blocking targets:

| Metric | Target |
|---|---:|
| Cyclomatic complexity | 10 |
| Cognitive complexity | 15 |
| Function length | 120 lines |
| Maximum nesting | 4 |

Warning-only targets are parameter count above 6, file length above 800, and duplicate-code candidates. Trend-only output includes test/production LOC ratio and TODO/FIXME count.

## Ratchet semantics

The checked-in baseline records metrics for Git-tracked production Go functions. Identity is repository-relative path plus receiver and function name.

- New functions must satisfy all blocking targets.
- Compliant functions may not become non-compliant.
- Historical over-limit functions may remain unchanged or improve.
- Any worsened blocking metric fails with baseline, current value, target, file, and function.
- Renamed over-limit functions are treated as new, so renaming cannot bypass the target.
- Baseline refresh is an explicit reviewed command; ordinary checks never rewrite it.

## Components

- scripts/quality/code-quality-ratchet.go calculates deterministic AST metrics and compares the baseline.
- scripts/quality/code-quality-ratchet.sh selects tracked changed Go files and emits warning/trend output.
- scripts/quality/code-quality-ratchet-test.sh uses mutation fixtures to prove the guard fails.
- scripts/quality/code-quality-baseline.json stores historical metrics.
- pre-commit, risk guard, Make, and CI call the same wrapper.

The analyzer excludes tests, generated files, vendor, testdata, local worktrees, and untracked files from production thresholds.

## Duplicate code

Duplicate detection is warning-only in version one because generated DTO and test patterns can create noisy clone reports. It may become blocking only after a reviewed baseline and false-positive audit.

## Stored-code refactoring

In parallel, refactor the pure Workflow validation functions validateNode and validInputValue. Preserve APIs, error behavior, validation order, and accepted values. The structural success condition is complexity at or below 10 for both target functions.

## Failure behavior

Missing tools, malformed baseline, parse failures, or Git resolution failures fail closed. No relevant Go changes succeeds explicitly. Warning metrics never alter the exit status in version one.

## Verification

Mutation fixtures cover new violations, worsened historical debt, renamed debt, improvements, exclusions, malformed baselines, and deterministic refresh. Repository verification includes risk guard tests, instruction generation tests, Workflow tests, lint, short Go tests, and short system E2E.

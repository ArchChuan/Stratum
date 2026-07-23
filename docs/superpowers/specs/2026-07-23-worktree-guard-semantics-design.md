# Worktree Guard Semantic Classification Design

## Goal

Eliminate recurring false-positive mutation blocks for read-only diagnostics in the Stratum primary checkout while
preserving its read-only role. In particular, allow compound read-only commands and network downloads whose only local
write target is an explicit safe path under `/tmp`.

## Scope

- Refine the shared worktree policy used by Codex and Claude Code.
- Extend the executable policy contract with regressions derived from observed false positives.
- Preserve linked-worktree target resolution and the existing platform adapters.
- Do not weaken repository write protection, Git state protection, remote mutation protection, or unknown-script
  handling.

The change does not attempt to prove arbitrary shell programs read-only. Unknown scripts, interpreters executing code,
and build tools remain fail-closed because a `PreToolUse` hook cannot establish their effects before execution.

## Policy Boundary

The primary checkout allows:

- commands with no concrete mutation signal;
- compound diagnostic commands when every command segment is allowed;
- no-op redirections to `/dev/null`;
- downloads using GET or HEAD semantics that write to stdout, `/dev/null`, or one explicit safe path under `/tmp`.

The primary checkout denies:

- writes to the repository or Git metadata;
- local write targets outside `/tmp`, except existing narrowly approved operations;
- ambiguous output targets containing variables, command substitutions, traversal, or glob syntax;
- HTTP uploads or mutating request methods;
- mixed compound commands when any segment is denied;
- unknown scripts or commands whose effects cannot be classified safely.

## Command Classification

Replace the current whole-command mutation decision with an ordered semantic classifier:

1. Resolve and validate the effective checkout exactly as the existing policy does.
2. Apply existing exact administrative exceptions, such as worktree creation and risk-guard explanation.
3. Split supported compound forms into command segments while retaining the operators between them.
4. Classify every segment by command family and redirection targets.
5. Allow the complete command only when every segment is allowed.

Supported composition initially covers pipelines, `&&`, and `||`. Semicolon-separated commands remain subject to the
same all-segments rule when they can be split without quoted or nested ambiguity. Unsupported or ambiguous shell syntax
fails closed.

This is a conservative shell lexer, not a general shell evaluator. It must preserve quoted text when identifying
operators and must reject unbalanced quotes, heredocs, process substitution, and unresolved command substitution.

## Network Commands

### curl

`curl` is read-only only when all of these conditions hold:

- the effective request method is GET or HEAD;
- no upload or request-body option is present, including `-d`, `--data*`, `-F`, `--form`, `-T`, and
  `--upload-file`;
- `-X` or `--request`, when present, specifies GET or HEAD;
- every output destination is stdout, `/dev/null`, or an explicit safe `/tmp` path;
- shell redirections satisfy the same destination rule.

Remote-name options are denied because their destination depends on remote input and the effective working directory.

### wget

`wget` is allowed only for ordinary retrieval with output to stdout, `/dev/null`, or an explicit safe `/tmp` path.
Recursive retrieval, remote-name-derived output, background execution, upload-like extension options, and destinations
outside `/tmp` are denied.

## Safe Temporary Targets

A safe temporary target:

- begins with `/tmp/` and names at least one path component below `/tmp`;
- contains no `..`, variable expansion, command substitution, backticks, or glob characters;
- is an explicit output file, not a directory inferred from remote content.

This validation reuses the existing safe temporary path rules so download and cleanup policy cannot drift.

## Error Handling

Denials identify the failing command segment and policy category without echoing credentials or complete sensitive
arguments. Categories distinguish repository mutation, unsafe local output, remote mutation, ambiguous shell syntax,
and unclassified execution. The message retains the effective working directory and worktree creation guidance where
applicable.

If parsing or classification is uncertain, the policy denies the command. A denial caused by unsupported syntax is
reported as such instead of being mislabeled as a generic mutation.

## Tests

Tests are added before implementation and cover both policy decisions and Codex/Claude adapter envelopes.

Allow cases include:

- the observed read-only search with `2>/dev/null`;
- multiple read-only diagnostics joined by pipelines and `&&`;
- GET/HEAD `curl` to stdout and `/dev/null`;
- `curl` and `wget` downloads to explicit nested `/tmp` files;
- a download followed by a read-only inspection of the same temporary file.

Deny cases include:

- output redirected into the primary checkout or another non-temporary directory;
- `/tmp` traversal, variable, command-substitution, and glob targets;
- POST, PUT, PATCH, DELETE, form upload, data upload, and file upload requests;
- remote-name and recursive downloads;
- a compound command containing one mutating segment;
- unknown scripts, heredocs, process substitution, and malformed quoting.

The full existing policy suite must continue to pass, including linked-worktree writes, Git mutation denials, bounded
temporary cleanup, PR operations, and both platform adapter contracts.

## Rollout And Recovery

The repository stores this design and the implementation plan. The active policy and executable test contract remain
under the user's local configuration. Before installation, copy the active files to timestamped backup paths outside
the repository. Install only after syntax checks and the complete contract suite pass against the candidate files.

After installation, rerun representative commands through both adapters. If any contract fails, restore the backups and
report the failed category; do not leave a partially updated policy active.

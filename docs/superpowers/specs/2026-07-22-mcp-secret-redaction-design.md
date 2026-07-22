# MCP Credential Redaction Design

## Goal

Prevent stored MCP credentials from crossing the backend-to-browser boundary while preserving a usable edit workflow.

## Scope

- Redact bearer tokens, API key values, and OAuth2 client secrets from configuration responses.
- Redact sensitive values embedded in MCP headers and environment variables.
- Restrict the configuration read endpoint to tenant administrators.
- Preserve existing credentials when an administrator submits an edit without a replacement value.
- Keep create behavior unchanged: credentials required by the selected authentication mode must still be supplied.

Moving credentials to Vault, KMS, or another secret manager is intentionally outside this fix.

## API Contract

`GET /mcp/servers/:id/config` returns a response DTO instead of serializing `domain.ServerConfig` directly. Authentication metadata may include a `credential_configured` boolean, but never returns `token`, `api_key_value`, or `oauth2_client_secret`.

Sensitive header and environment entries are omitted from the response. A field-name match is case-insensitive and covers authorization, API keys, tokens, secrets, passwords, and credentials. Non-sensitive entries remain editable.

The endpoint uses the same administrator middleware as MCP create, update, reconnect, and delete operations.

## Update Semantics

Before updating an existing server, the application loads its stored configuration and merges protected values:

- An omitted or empty credential for the unchanged authentication type preserves the stored credential.
- A non-empty credential replaces the stored credential.
- Switching authentication type discards credentials belonging to the previous type.
- Removing authentication discards the previous authentication configuration.
- Sensitive header and environment entries omitted by the redacted read response are preserved when their corresponding configuration section remains applicable.
- An explicitly supplied sensitive header or environment entry replaces the stored value.

The merge occurs on the backend so clients cannot accidentally erase a secret they were never allowed to read.

## Frontend Behavior

The edit form never receives or stores an existing credential. When `credential_configured` is true, the relevant secret field is optional and explains that leaving it empty preserves the existing value. Entering a value replaces it. Create forms continue to require credentials.

## Verification

- Handler tests prove JSON responses contain no stored credentials or sensitive header/environment values.
- Route tests prove members cannot read configuration and administrators can.
- Application tests prove preserve, replace, authentication-switch, and removal behavior.
- Frontend tests prove redacted configurations do not populate secret inputs and produce omission semantics on update.
- A real API check verifies the response body and role boundary without printing credentials.
- A browser E2E check edits a server without replacing its credential, reloads it, and confirms the configured state remains visible without exposing the original value.

package domain

import "errors"

// ErrNameConflict is returned when an MCP server with the same name already exists in the tenant.
var ErrNameConflict = errors.New("mcp server name already exists")

// ErrServerNotFound is returned when an MCP server lookup misses.
var ErrServerNotFound = errors.New("mcp server not found")

// ErrPlatformManagedServer rejects tenant lifecycle changes to the built-in MCP server.
var ErrPlatformManagedServer = errors.New("platform-managed MCP server cannot be changed")

// ErrConnectionLimitExceeded is returned when the global or per-tenant MCP connection cap is hit.
var ErrConnectionLimitExceeded = errors.New("MCP connection limit exceeded")

// ErrForbidden is returned when the actor may not modify a resource they did not create.
var ErrForbidden = errors.New("resource ownership forbidden")

// ErrEditorNotEligible rejects a granted editor who does not hold role
// admin or owner at write time (fail closed, prevents forgery).
var ErrEditorNotEligible = errors.New("editor must hold admin or owner role")

// ErrUnsupportedTransport rejects MCP server configs whose transport is not
// supported. Tenant stdio is disabled platform-wide: spawning arbitrary
// processes from tenant-editable config is an arbitrary command execution
// vector (the sandbox cgroup config was declared but never wired). Only
// http/streamable-http transports are accepted.
var ErrUnsupportedTransport = errors.New("unsupported MCP transport")

// ErrInvalidServerURL rejects MCP server URLs that fail the SSRF policy
// (scheme outside http/https, userinfo, private/loopback/metadata targets).
var ErrInvalidServerURL = errors.New("invalid MCP server URL")

// ErrUnsupportedAuth rejects MCP server configs declaring an auth type the
// client does not support (OAuth2 is out of scope: the configured
// client-credentials model does not align with the SDK OAuth flow).
var ErrUnsupportedAuth = errors.New("unsupported MCP auth type")

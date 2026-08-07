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

package domain

import "errors"

// ErrNameConflict is returned when an MCP server with the same name already exists in the tenant.
var ErrNameConflict = errors.New("mcp server name already exists")

// ErrServerNotFound is returned when an MCP server lookup misses.
var ErrServerNotFound = errors.New("mcp server not found")

// ErrSkillNotFound is returned when an MCP skill lookup misses.
var ErrSkillNotFound = errors.New("mcp skill not found")

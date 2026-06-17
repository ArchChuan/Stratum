package domain

import "errors"

// ErrNameConflict is returned when an MCP server with the same name already exists in the tenant.
var ErrNameConflict = errors.New("mcp server name already exists")

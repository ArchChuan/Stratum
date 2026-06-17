// Package domain holds mcp context entities.
package domain

type Server struct {
	ID, Name, URL string
}

type Tool struct {
	Name, Description string
}

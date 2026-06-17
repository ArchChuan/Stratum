// Package domain holds skill context entities.
package domain

type Kind string

const (
	KindHTTP Kind = "http"
	KindLLM  Kind = "llm"
	KindCode Kind = "code"
)

type Skill struct {
	ID, Name, Description string
	Kind                  Kind
}

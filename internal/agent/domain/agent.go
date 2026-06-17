// Package domain holds agent context entities. Phase 4 will migrate
// real fields and behavior here from internal/agent/*.go.
package domain

type Agent struct {
	ID, Name, Description string
}

type Execution struct {
	ID string
}

type Conversation struct {
	ID string
}

type ChatMessage struct {
	ID, Role, Content string
}

type ListFilter struct {
	Limit, Offset int
}

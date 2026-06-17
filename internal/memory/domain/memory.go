// Package domain holds memory context entities.
package domain

type Entry struct {
	ID, TenantID, UserID, Content string
	Importance                    float32
}

type Entity struct {
	ID, Name, Type string
}

type SessionState struct {
	ID, TenantID string
}

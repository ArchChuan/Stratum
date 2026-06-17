// Package domain holds iam context entities.
package domain

type User struct {
	ID, Email, TenantID string
}

type Token struct {
	Hash, UserID, TenantID string
}

type Session struct {
	ID, UserID string
}

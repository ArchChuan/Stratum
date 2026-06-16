package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// chatPoolIface is the minimal pool surface needed by PgChatStore (allows mock injection in tests).
type chatPoolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ChatConversation is a named conversation thread between a user and an agent.
type ChatConversation struct {
	ID        string
	AgentID   string
	UserID    string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
	DeletedAt *time.Time
}

// ChatMessage is a single message in a conversation.
type ChatMessage struct {
	ID             string
	ConversationID string
	Role           string // "user" | "agent"
	Content        string
	StepsJSON      json.RawMessage
	IsError        bool
	CreatedAt      time.Time
	UserID         string
	AgentID        string
}

// ChatStore persists chat conversations and messages in the per-tenant schema.
type ChatStore interface {
	CreateConversation(ctx context.Context, tenantID, agentID, userID, name string) (*ChatConversation, error)
	ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*ChatConversation, error)
	RenameConversation(ctx context.Context, tenantID, convID, userID, name string) error
	DeleteConversation(ctx context.Context, tenantID, convID, userID string) error
	AddMessage(ctx context.Context, tenantID string, msg *ChatMessage) error
	ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*ChatMessage, error)
	CleanupExpired(ctx context.Context, tenantID string) error
}

// PgChatStore implements ChatStore using pgxpool with per-tenant search_path.
type PgChatStore struct {
	pool chatPoolIface
}

// NewPgChatStore creates a PgChatStore.
func NewPgChatStore(pool *pgxpool.Pool) *PgChatStore {
	return &PgChatStore{pool: pool}
}

// execTenantID runs fn in a transaction scoped to tenant_{id}'s search_path.
func execTenantID(ctx context.Context, pool chatPoolIface, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if tenantID == "" {
		return fmt.Errorf("chat_store: empty tenant_id")
	}
	for _, r := range tenantID {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return fmt.Errorf("chat_store: invalid tenant_id %q", tenantID)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("chat_store: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	schema := "tenant_" + tenantID
	if _, err = tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "%s", public`, schema)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("chat_store: set search_path: %w", err)
	}
	if err = fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgChatStore) CreateConversation(ctx context.Context, tenantID, agentID, userID, name string) (*ChatConversation, error) {
	var conv ChatConversation
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO chat_conversations (agent_id, user_id, name)
			 VALUES ($1, $2, $3)
			 RETURNING id, agent_id, user_id, name, created_at, updated_at, expires_at`,
			agentID, userID, name,
		).Scan(&conv.ID, &conv.AgentID, &conv.UserID, &conv.Name,
			&conv.CreatedAt, &conv.UpdatedAt, &conv.ExpiresAt)
	})
	if err != nil {
		return nil, fmt.Errorf("chat_store: create conversation: %w", err)
	}
	return &conv, nil
}

func (s *PgChatStore) ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*ChatConversation, error) {
	var out []*ChatConversation
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, agent_id, user_id, name, created_at, updated_at, expires_at
			 FROM chat_conversations
			 WHERE agent_id = $1 AND user_id = $2 AND expires_at > NOW() AND deleted_at IS NULL
			 ORDER BY updated_at DESC`,
			agentID, userID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c ChatConversation
			if err := rows.Scan(&c.ID, &c.AgentID, &c.UserID, &c.Name,
				&c.CreatedAt, &c.UpdatedAt, &c.ExpiresAt); err != nil {
				return err
			}
			out = append(out, &c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("chat_store: list conversations: %w", err)
	}
	return out, nil
}

func (s *PgChatStore) RenameConversation(ctx context.Context, tenantID, convID, userID, name string) error {
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE chat_conversations SET name=$1, updated_at=NOW()
			 WHERE id=$2 AND user_id=$3`,
			name, convID, userID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("chat_store: rename conversation: %w", err)
	}
	return nil
}

func (s *PgChatStore) DeleteConversation(ctx context.Context, tenantID, convID, userID string) error {
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE chat_conversations SET deleted_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`,
			convID, userID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("chat_store: delete conversation: %w", err)
	}
	return nil
}

func (s *PgChatStore) AddMessage(ctx context.Context, tenantID string, msg *ChatMessage) error {
	if msg.StepsJSON == nil {
		msg.StepsJSON = json.RawMessage("[]")
	}
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		// bump expires_at on the conversation (rolling 30-day expiry)
		if _, err := tx.Exec(ctx,
			`UPDATE chat_conversations
			 SET updated_at=NOW(), expires_at=NOW()+INTERVAL '30 days'
			 WHERE id=$1`,
			msg.ConversationID,
		); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO chat_messages (conversation_id, role, content, steps_json, is_error)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id, created_at`,
			msg.ConversationID, msg.Role, msg.Content, msg.StepsJSON, msg.IsError,
		).Scan(&msg.ID, &msg.CreatedAt); err != nil {
			return err
		}

		outboxPayload, err := json.Marshal(map[string]interface{}{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"tenant_id":       tenantID,
			"role":            msg.Role,
			"content":         msg.Content,
			"created_at":      msg.CreatedAt,
			"user_id":         msg.UserID,
			"agent_id":        msg.AgentID,
		})
		if err != nil {
			return fmt.Errorf("marshal outbox payload: %w", err)
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO memory_outbox (message_id, payload) VALUES ($1, $2)`,
			msg.ID, outboxPayload)
		if err != nil {
			return fmt.Errorf("insert memory_outbox: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("chat_store: add message: %w", err)
	}
	return nil
}

func (s *PgChatStore) ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*ChatMessage, error) {
	var out []*ChatMessage
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT m.id, m.conversation_id, m.role, m.content, m.steps_json, m.is_error, m.created_at
			 FROM chat_messages m
			 JOIN chat_conversations c ON c.id = m.conversation_id
			 WHERE m.conversation_id = $1 AND c.user_id = $2 AND c.deleted_at IS NULL
			 ORDER BY m.created_at ASC`,
			convID, userID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m ChatMessage
			if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content,
				&m.StepsJSON, &m.IsError, &m.CreatedAt); err != nil {
				return err
			}
			out = append(out, &m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("chat_store: list messages: %w", err)
	}
	return out, nil
}

func (s *PgChatStore) CleanupExpired(ctx context.Context, tenantID string) error {
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM chat_conversations
			 WHERE expires_at < NOW()
			    OR (deleted_at IS NOT NULL AND deleted_at < NOW() - INTERVAL '30 days')`)
		return err
	})
	if err != nil {
		return fmt.Errorf("chat_store: cleanup expired: %w", err)
	}
	return nil
}

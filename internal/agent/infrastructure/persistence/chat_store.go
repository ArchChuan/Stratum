// Package persistence holds Postgres adapters for the agent context.
//
// All SQL lives here, behind interfaces declared in
// internal/agent/domain/port. Application code depends on those ports,
// never on pgx.

package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/safetext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// chatPoolIface is the minimal pool surface needed by PgChatStore (allows pgxmock injection).
type chatPoolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// PgChatStore implements port.ChatRepo using pgxpool with per-tenant search_path.
type PgChatStore struct {
	pool   chatPoolIface
	logger *zap.Logger
}

// NewPgChatStore creates a PgChatStore. If logger is nil, a no-op logger is used.
func NewPgChatStore(pool *pgxpool.Pool, logger *zap.Logger) *PgChatStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PgChatStore{pool: pool, logger: logger}
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

func (s *PgChatStore) GetConversation(ctx context.Context, tenantID, convID string) (*domain.ChatConversation, error) {
	var conv domain.ChatConversation
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, agent_id, user_id, name, created_at, updated_at, expires_at
			 FROM chat_conversations WHERE id = $1 AND deleted_at IS NULL`,
			convID,
		).Scan(&conv.ID, &conv.AgentID, &conv.UserID, &conv.Name,
			&conv.CreatedAt, &conv.UpdatedAt, &conv.ExpiresAt)
	})
	if err != nil {
		return nil, fmt.Errorf("chat_store: get conversation: %w", err)
	}
	return &conv, nil
}

func (s *PgChatStore) CreateConversation(ctx context.Context, tenantID, agentID, userID, name string) (*domain.ChatConversation, error) {
	var conv domain.ChatConversation
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

func (s *PgChatStore) ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*domain.ChatConversation, error) {
	var out []*domain.ChatConversation
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
			var c domain.ChatConversation
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
			return domain.ErrNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return fmt.Errorf("chat_store: rename conversation: %w", err)
	}
	return nil
}

func (s *PgChatStore) DeleteConversation(ctx context.Context, tenantID, convID, userID string) error {
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM chat_messages WHERE conversation_id = $1`,
			convID,
		); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`DELETE FROM chat_conversations WHERE id = $1 AND user_id = $2`,
			convID, userID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return fmt.Errorf("chat_store: delete conversation: %w", err)
	}
	return nil
}

func (s *PgChatStore) AddMessage(ctx context.Context, tenantID string, msg *domain.ChatMessage) error {
	if msg.StepsJSON == nil {
		msg.StepsJSON = json.RawMessage("[]")
	}
	if msg.Artifacts == nil {
		msg.Artifacts = []domain.ExecutionArtifact{}
	}
	artifactsJSON, err := encodeExecutionArtifacts(msg.Artifacts)
	if err != nil {
		return fmt.Errorf("chat_store: encode artifacts: %w", err)
	}
	var outboxQueued bool
	var outboxSkipReason string
	err = execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE chat_conversations
			 SET updated_at=NOW(), expires_at=NOW()+INTERVAL '30 days'
			 WHERE id=$1`,
			msg.ConversationID,
		); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO chat_messages (conversation_id, role, content, steps_json, is_error, artifacts_json)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id, created_at`,
			msg.ConversationID, msg.Role, msg.Content, string(msg.StepsJSON), msg.IsError, string(artifactsJSON),
		).Scan(&msg.ID, &msg.CreatedAt); err != nil {
			return err
		}

		outboxContent := memoryOutboxContent(msg.Role, msg.Content)
		if outboxContent == "" {
			outboxSkipReason = describeOutboxSkip(msg.Role, msg.Content)
			return nil
		}
		if msg.IsError {
			outboxSkipReason = "is_error"
			return nil
		}
		if msg.SkipOutbox {
			outboxSkipReason = "memory_disabled"
			return nil
		}
		outboxPayload, err := json.Marshal(map[string]any{
			"message_id":      msg.ID,
			"conversation_id": msg.ConversationID,
			"tenant_id":       tenantID,
			"role":            msg.Role,
			"content":         outboxContent,
			"created_at":      msg.CreatedAt,
			"user_id":         msg.UserID,
			"agent_id":        msg.AgentID,
			"scope":           msg.MemoryScope,
		})
		if err != nil {
			return fmt.Errorf("marshal outbox payload: %w", err)
		}
		if _, err = tx.Exec(ctx,
			`INSERT INTO memory_outbox (message_id, user_id, agent_id, payload) VALUES ($1, $2, $3, $4)`,
			msg.ID, msg.UserID, msg.AgentID, string(outboxPayload)); err != nil {
			return fmt.Errorf("insert memory_outbox: %w", err)
		}
		outboxQueued = true
		return nil
	})
	if err != nil {
		s.logger.Warn("chat.outbox.tx_failed",
			zap.String("tenant_id", tenantID),
			zap.String("conversation_id", msg.ConversationID),
			zap.String("role", msg.Role),
			zap.Error(err))
		return fmt.Errorf("chat_store: add message: %w", err)
	}
	if outboxQueued {
		s.logger.Info("chat.outbox.queued",
			zap.String("tenant_id", tenantID),
			zap.String("conversation_id", msg.ConversationID),
			zap.String("message_id", msg.ID),
			zap.String("role", msg.Role),
			zap.Int("content_runes", len([]rune(msg.Content))))
	} else {
		s.logger.Debug("chat.outbox.skip",
			zap.String("tenant_id", tenantID),
			zap.String("conversation_id", msg.ConversationID),
			zap.String("message_id", msg.ID),
			zap.String("role", msg.Role),
			zap.String("reason", outboxSkipReason))
	}
	return nil
}

// describeOutboxSkip returns a human-readable reason explaining why
// memoryOutboxContent rejected the message — used purely for diagnostic logs.
func describeOutboxSkip(role, content string) string {
	if role != "user" && role != "assistant" {
		return "role_not_persistable:" + role
	}
	runes := []rune(content)
	if len(runes) < constants.MemoryOutboxMinRunes {
		return fmt.Sprintf("content_too_short:%d<%d", len(runes), constants.MemoryOutboxMinRunes)
	}
	return "unknown"
}

func (s *PgChatStore) ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*domain.ChatMessage, error) {
	var out []*domain.ChatMessage
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT m.id, m.conversation_id, m.role, m.content, m.steps_json, m.is_error, m.created_at, m.artifacts_json
			 FROM chat_messages m
			 JOIN chat_conversations c ON c.id = m.conversation_id
			 WHERE m.conversation_id = $1 AND c.user_id = $2 AND c.deleted_at IS NULL
			   AND m.role IN ('user', 'assistant')
			 ORDER BY m.created_at ASC`,
			convID, userID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m domain.ChatMessage
			var artifactsJSON []byte
			if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content,
				&m.StepsJSON, &m.IsError, &m.CreatedAt, &artifactsJSON); err != nil {
				return err
			}
			m.Artifacts, err = decodeExecutionArtifacts(artifactsJSON)
			if err != nil {
				return fmt.Errorf("decode message artifacts: %w", err)
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

func decodeExecutionArtifacts(raw []byte) ([]domain.ExecutionArtifact, error) {
	if len(raw) > constants.SystemAssistantToolMaxJSONBytes {
		return nil, errors.New("artifacts exceed persisted size limit")
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil, errors.New("artifacts must be an array")
	}
	var artifacts []domain.ExecutionArtifact
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifacts); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("artifacts contain trailing JSON")
	}
	if artifacts == nil {
		return nil, errors.New("artifacts must be a non-null array")
	}
	for i := range artifacts {
		artifact := &artifacts[i]
		if artifact.ProfileVersion == "" || len([]rune(artifact.ProfileVersion)) > constants.SystemAssistantEvidenceFieldMaxRunes || safetext.RedactCredentials(artifact.ProfileVersion) != artifact.ProfileVersion {
			return nil, fmt.Errorf("artifact %d has invalid profile version", i)
		}
		switch artifact.Type {
		case "citations":
			if artifact.DiagnosticReport != nil || artifact.Citations == nil {
				return nil, fmt.Errorf("artifact %d has invalid citation fields", i)
			}
			if err := validateArtifactCitations(artifact.Citations); err != nil {
				return nil, fmt.Errorf("artifact %d: %w", i, err)
			}
		case "diagnostic_report":
			if artifact.DiagnosticReport == nil || artifact.Citations != nil {
				return nil, fmt.Errorf("artifact %d has invalid diagnostic report", i)
			}
			normalizeDiagnosticReport(artifact.DiagnosticReport)
			if err := validateDiagnosticReport(artifact.DiagnosticReport); err != nil {
				return nil, fmt.Errorf("artifact %d: %w", i, err)
			}
		default:
			return nil, fmt.Errorf("artifact %d has invalid type", i)
		}
	}
	return artifacts, nil
}

var artifactCodePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func encodeExecutionArtifacts(artifacts []domain.ExecutionArtifact) ([]byte, error) {
	raw, err := json.Marshal(artifacts)
	if err != nil {
		return nil, err
	}
	validated, err := decodeExecutionArtifacts(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(validated)
}

func validateDiagnosticReport(report *domain.DiagnosticReport) error {
	if err := validateArtifactCitations(report.Citations); err != nil {
		return err
	}
	if len(report.Inferences) != 0 {
		return errors.New("phase 1 diagnostic inferences must be empty")
	}
	for _, action := range report.RecommendedActions {
		if !safeArtifactString(action) {
			return errors.New("invalid recommended action")
		}
	}
	for _, fact := range report.Facts {
		if !fact.Area.Valid() || fact.Statement == "" || fact.Source == "" || !safeArtifactString(fact.ObjectID) || !safeArtifactString(fact.Statement) || !safeArtifactString(fact.Source) {
			return errors.New("invalid diagnostic fact")
		}
	}
	for _, gap := range report.EvidenceGaps {
		if (gap.Area != "" && !gap.Area.Valid()) || !artifactCodePattern.MatchString(gap.Code) || !safeArtifactString(gap.Source) {
			return errors.New("invalid evidence gap")
		}
	}
	for _, step := range report.Steps {
		if step.Tool == "" || !validArtifactOutcome(step.Outcome) || step.LatencyMs < 0 || !safeArtifactString(step.Tool) || (step.ErrorCode != "" && !artifactCodePattern.MatchString(step.ErrorCode)) {
			return errors.New("invalid diagnostic step")
		}
	}
	return nil
}

func validArtifactOutcome(outcome string) bool {
	switch outcome {
	case "success", "error", "gap", "truncated":
		return true
	default:
		return false
	}
}

func validateArtifactCitations(citations []domain.Citation) error {
	for _, citation := range citations {
		if citation.DocumentID == "" || !safeArtifactString(citation.DocumentID) || !safeArtifactString(citation.Title) || !safeArtifactString(citation.ProductVersion) || !safeArtifactString(citation.Section) || !safeArtifactString(citation.URL) || !safeArtifactString(citation.Excerpt) {
			return errors.New("invalid citation")
		}
	}
	return nil
}

func safeArtifactString(value string) bool {
	return len([]rune(value)) <= constants.SystemAssistantEvidenceFieldMaxRunes && safetext.RedactCredentials(value) == value
}

func normalizeDiagnosticReport(report *domain.DiagnosticReport) {
	if report.Facts == nil {
		report.Facts = []domain.DiagnosticFact{}
	}
	if report.Inferences == nil {
		report.Inferences = []string{}
	}
	if report.EvidenceGaps == nil {
		report.EvidenceGaps = []domain.EvidenceGap{}
	}
	if report.RecommendedActions == nil {
		report.RecommendedActions = []string{}
	}
	if report.Citations == nil {
		report.Citations = []domain.Citation{}
	}
	if report.Steps == nil {
		report.Steps = []domain.DiagnosticStep{}
	}
}

func (s *PgChatStore) DeleteByAgent(ctx context.Context, tenantID, agentID string) error {
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM chat_messages WHERE conversation_id IN (SELECT id FROM chat_conversations WHERE agent_id = $1)`,
			agentID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM chat_conversations WHERE agent_id = $1`, agentID)
		return err
	})
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

// memoryOutboxContent decides whether a message should be written to memory_outbox
// and returns the (possibly truncated) content to store.
//
// Rules (industry-standard lightweight pre-filter):
//   - role must be "user" or "assistant" — system/tool messages are internal signals
//   - content must be at least MemoryOutboxMinRunes runes — short acks carry no value
//   - content is truncated to MemoryOutboxMaxRunes runes — prevents noisy oversized vectors
//
// Returns "" when the message should be skipped.
func memoryOutboxContent(role, content string) string {
	if role != "user" && role != "assistant" {
		return ""
	}
	runes := []rune(content)
	if len(runes) < constants.MemoryOutboxMinRunes {
		return ""
	}
	if len(runes) > constants.MemoryOutboxMaxRunes {
		return string(runes[:constants.MemoryOutboxMaxRunes])
	}
	return content
}

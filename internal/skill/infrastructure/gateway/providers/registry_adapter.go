package providers

import (
	"context"
	"encoding/json"
	"fmt"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type DBSkillAdapter struct {
	pool      *pgxpool.Pool
	completer llmgateway.LLMCompleter
	logger    *zap.Logger
	executor  *code.CodeExecutor
}

func NewDBSkillAdapter(pool *pgxpool.Pool, completer llmgateway.LLMCompleter, logger *zap.Logger, executor *code.CodeExecutor) *DBSkillAdapter {
	return &DBSkillAdapter{pool: pool, completer: completer, logger: logger, executor: executor}
}

// SkillIDs returns nil — no precomputed index; Resolve falls through to Has().
func (a *DBSkillAdapter) SkillIDs() []string { return nil }

// Has always returns true; Execute returns an error for unknown IDs.
func (a *DBSkillAdapter) Has(_ string) bool { return true }

func (a *DBSkillAdapter) SkillType() string { return "db" }

func (a *DBSkillAdapter) Execute(ctx context.Context, skillID string, input any) (any, error) {
	var s domain.Skill
	if err := tenantdb.ExecTenant(ctx, a.pool, func(ctx context.Context, tx pgx.Tx) error {
		var id, name, desc, skillType string
		var cfgJSON []byte
		if err := tx.QueryRow(ctx,
			`SELECT id, name, description, type, config FROM skills WHERE id=$1`, skillID,
		).Scan(&id, &name, &desc, &skillType, &cfgJSON); err != nil {
			return fmt.Errorf("skill not found: %s", skillID)
		}
		var cfg map[string]any
		_ = json.Unmarshal(cfgJSON, &cfg)
		var err error
		s, err = buildSkill(id, name, desc, skillType, cfg, a.completer, a.logger, a.executor)
		return err
	}); err != nil {
		return nil, err
	}
	executor, ok := s.(domain.SkillExecutor)
	if !ok {
		return nil, fmt.Errorf("skill %s does not implement SkillExecutor", skillID)
	}
	return executor.Execute(ctx, input)
}

func buildSkill(id, name, desc, skillType string, cfg map[string]any, c llmgateway.LLMCompleter, logger *zap.Logger, executor *code.CodeExecutor) (domain.Skill, error) {
	switch skillType {
	case "http":
		headers := map[string]string{}
		if h, ok := cfg["headers"].(map[string]any); ok {
			for k, v := range h {
				if sv, ok := v.(string); ok {
					headers[k] = sv
				}
			}
		}
		return executors.NewHTTPSkill(id, name, desc,
			stringVal(cfg, "url"), stringVal(cfg, "method"),
			headers, stringVal(cfg, "body_template"), intVal(cfg, "timeout_sec"),
		)
	case "llm":
		return executors.NewLLMSkill(id, name, desc,
			stringVal(cfg, "system_prompt"), stringVal(cfg, "model"),
			float32Val(cfg, "temperature"), intVal(cfg, "max_tokens"),
			c, logger,
		), nil
	case "code":
		return code.NewCodeSkillWithExecutor(id, name, desc,
			stringVal(cfg, "code"), stringVal(cfg, "language"), executor,
		), nil
	default:
		return nil, fmt.Errorf("unknown skill type: %s", skillType)
	}
}

func stringVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intVal(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func float32Val(m map[string]any, key string) float32 {
	if v, ok := m[key].(float64); ok {
		return float32(v)
	}
	return 0
}

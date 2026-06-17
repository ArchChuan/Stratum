// Package handler implements HTTP API request handlers.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	skillinfra "github.com/byteBuilderX/stratum/internal/skill/infrastructure"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors"
	"github.com/byteBuilderX/stratum/internal/skill/infrastructure/executors/code"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type configurable interface {
	GetConfig() map[string]any
}

type SkillHandler struct {
	pool     *pgxpool.Pool
	logger   *zap.Logger
	gateway  *llmgateway.Gateway
	executor *code.CodeExecutor
	analyzer skillinfra.StaticAnalyzer
}

func NewSkillHandler(pool *pgxpool.Pool, logger *zap.Logger, gateway *llmgateway.Gateway, executor *code.CodeExecutor) *SkillHandler {
	return &SkillHandler{
		pool:     pool,
		logger:   logger,
		gateway:  gateway,
		executor: executor,
		analyzer: skillinfra.NewStaticAnalyzer(),
	}
}

func (h *SkillHandler) CreateSkill(c *gin.Context) {
	var req dto.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	id := uuid.New().String()
	s, err := h.buildSkillFromRequest(id, req)
	if err != nil {
		var aErr *analysisError
		if errors.As(err, &aErr) {
			c.JSON(http.StatusBadRequest, dto.SkillResponse{AnalysisErrors: aErr.reasons})
			return
		}
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	cfgJSON, _ := json.Marshal(skillConfig(s))

	var createdAt time.Time
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO skills (id, name, description, type, config)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING created_at`,
			id, s.GetName(), s.GetDescription(), s.GetType(), cfgJSON,
		).Scan(&createdAt)
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, dto.ErrorResponse{Code: http.StatusConflict, Message: "skill name already exists"})
			return
		}
		h.logger.Error("failed to create skill", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: http.StatusInternalServerError, Message: "failed to create skill"})
		return
	}
	h.logger.Info("skill created", zap.String("id", id), zap.String("name", req.Name))
	c.JSON(http.StatusCreated, skillRow{id: id, name: s.GetName(), desc: s.GetDescription(), typ: s.GetType(), cfg: skillConfig(s), createdAt: createdAt}.toResponse())
}

func (h *SkillHandler) GetSkill(c *gin.Context) {
	id := c.Param("id")
	var row skillRow
	found := false
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		var cfgJSON []byte
		err := tx.QueryRow(ctx,
			`SELECT id, name, description, type, config, created_at FROM skills WHERE id=$1`, id,
		).Scan(&row.id, &row.name, &row.desc, &row.typ, &cfgJSON, &row.createdAt)
		if err != nil {
			return err
		}
		found = true
		return json.Unmarshal(cfgJSON, &row.cfg)
	}); err != nil || !found {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: http.StatusNotFound, Message: "skill not found"})
		return
	}
	c.JSON(http.StatusOK, row.toResponse())
}

func (h *SkillHandler) GetAllSkills(c *gin.Context) {
	var rows []skillRow
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		pgRows, err := tx.Query(ctx, `SELECT id, name, description, type, config, created_at FROM skills ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer pgRows.Close()
		for pgRows.Next() {
			var r skillRow
			var cfgJSON []byte
			if err := pgRows.Scan(&r.id, &r.name, &r.desc, &r.typ, &cfgJSON, &r.createdAt); err != nil {
				return err
			}
			_ = json.Unmarshal(cfgJSON, &r.cfg)
			rows = append(rows, r)
		}
		return pgRows.Err()
	}); err != nil {
		h.logger.Error("failed to list skills", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: http.StatusInternalServerError, Message: "failed to list skills"})
		return
	}
	responses := make([]dto.SkillResponse, 0, len(rows))
	for _, r := range rows {
		responses = append(responses, r.toResponse())
	}
	c.JSON(http.StatusOK, gin.H{"skills": responses})
}

func (h *SkillHandler) UpdateSkill(c *gin.Context) {
	id := c.Param("id")

	var req dto.CreateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("invalid request", zap.Error(err))
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	// Validate type hasn't changed and build new config.
	var existingType string
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT type FROM skills WHERE id=$1`, id).Scan(&existingType)
	}); err != nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: http.StatusNotFound, Message: "skill not found"})
		return
	}
	if existingType != req.Type {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: "cannot change skill type"})
		return
	}

	s, err := h.buildSkillFromRequest(id, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	cfgJSON, _ := json.Marshal(skillConfig(s))

	var createdAt time.Time
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`UPDATE skills SET name=$2, description=$3, type=$4, config=$5 WHERE id=$1 RETURNING created_at`,
			id, s.GetName(), s.GetDescription(), s.GetType(), cfgJSON,
		).Scan(&createdAt)
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, dto.ErrorResponse{Code: http.StatusConflict, Message: "skill name already exists"})
			return
		}
		h.logger.Error("failed to update skill", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: http.StatusInternalServerError, Message: "failed to update skill"})
		return
	}
	h.logger.Info("skill updated", zap.String("id", id))
	c.JSON(http.StatusOK, skillRow{id: id, name: s.GetName(), desc: s.GetDescription(), typ: s.GetType(), cfg: skillConfig(s), createdAt: createdAt}.toResponse())
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	id := c.Param("id")
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM skills WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errSkillNotFound
		}
		return nil
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			c.JSON(http.StatusConflict, dto.ErrorResponse{Code: http.StatusConflict, Message: "skill still linked to agents"})
			return
		}
		if errors.Is(err, errSkillNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: http.StatusNotFound, Message: "skill not found"})
			return
		}
		h.logger.Error("failed to delete skill", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: http.StatusInternalServerError, Message: "failed to delete skill"})
		return
	}
	h.logger.Info("skill deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"message": "skill deleted successfully"})
}

// RunSkill executes a code skill on demand.
// POST /skills/:id/run
func (h *SkillHandler) RunSkill(c *gin.Context) {
	id := c.Param("id")

	var cfgJSON []byte
	if err := tenantdb.ExecTenant(c.Request.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		var typ string
		if err := tx.QueryRow(ctx,
			`SELECT type, config FROM skills WHERE id=$1`, id,
		).Scan(&typ, &cfgJSON); err != nil {
			return errSkillNotFound
		}
		if typ != "code" {
			return errNotCodeSkill
		}
		return nil
	}); err != nil {
		if errors.Is(err, errSkillNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Code: http.StatusNotFound, Message: "skill not found"})
			return
		}
		if errors.Is(err, errNotCodeSkill) {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: "skill is not a code skill"})
			return
		}
		h.logger.Error("failed to load skill for run", zap.Error(err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Code: http.StatusInternalServerError, Message: "failed to load skill"})
		return
	}

	var cfg map[string]any
	_ = json.Unmarshal(cfgJSON, &cfg)
	cs := code.NewCodeSkillWithExecutor(id, "", "", stringCfgVal(cfg, "code"), stringCfgVal(cfg, "language"), h.executor)

	var req dto.RunSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Code: http.StatusBadRequest, Message: err.Error()})
		return
	}

	tenantID, _ := c.Get("tenant_id")
	input := req.Input
	if input == nil {
		input = make(map[string]interface{})
	}
	if tid, ok := tenantID.(string); ok && tid != "" {
		input["__tenant_id"] = tid
	}

	start := time.Now()
	out, err := cs.Execute(c.Request.Context(), input)
	if err != nil {
		if errors.Is(err, skillinfra.ErrConcurrencyLimit) {
			c.JSON(http.StatusTooManyRequests, dto.RunSkillResponse{Error: "concurrency limit reached"})
			return
		}
		c.JSON(http.StatusInternalServerError, dto.RunSkillResponse{Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, dto.RunSkillResponse{Output: out})
	h.logger.Info("skill executed",
		zap.String("id", id),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()),
	)
}

// buildSkillFromRequest constructs a Skill object from the request, performing validation.
func (h *SkillHandler) buildSkillFromRequest(id string, req dto.CreateSkillRequest) (domain.Skill, error) {
	switch req.Type {
	case "code":
		if result := h.analyzer.Check(req.Language, req.Code); !result.Safe {
			return nil, &analysisError{reasons: result.Reasons}
		}
		return code.NewCodeSkillWithExecutor(id, req.Name, req.Description, req.Code, req.Language, h.executor), nil
	case "llm":
		return executors.NewLLMSkill(id, req.Name, req.Description, req.SystemPrompt, req.Model, req.Temperature, req.MaxTokens, h.gateway, h.logger), nil
	case "http":
		return executors.NewHTTPSkill(id, req.Name, req.Description, req.URL, req.Method, req.Headers, req.BodyTemplate, req.TimeoutSec)
	default:
		return nil, fmt.Errorf("unsupported skill type: %s", req.Type)
	}
}

// skillConfig extracts the config map from a Skill.
func skillConfig(s domain.Skill) map[string]any {
	if c, ok := s.(configurable); ok {
		return c.GetConfig()
	}
	return map[string]any{}
}

func stringCfgVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

var (
	errSkillNotFound = errors.New("skill not found")
	errNotCodeSkill  = errors.New("not a code skill")
)

type analysisError struct{ reasons []string }

func (e *analysisError) Error() string { return "code analysis failed" }

// skillRow holds raw DB columns for building responses.
type skillRow struct {
	id        string
	name      string
	desc      string
	typ       string
	cfg       map[string]any
	createdAt time.Time
}

func (r skillRow) toResponse() dto.SkillResponse {
	return dto.SkillResponse{
		ID:          r.id,
		Name:        r.name,
		Description: r.desc,
		Type:        r.typ,
		Config:      r.cfg,
		CreatedAt:   r.createdAt.Format(time.RFC3339),
	}
}

// Package handler implements HTTP API request handlers.

package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// PgxPool is the minimal pgxpool interface used by admin and tenant handlers.
type PgxPool interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

// Ensure *pgxpool.Pool satisfies PgxPool at compile time.
var _ PgxPool = (*pgxpool.Pool)(nil)

type AdminHandler struct {
	db     PgxPool
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func NewAdminHandler(db PgxPool, logger *zap.Logger) *AdminHandler {
	pool, _ := db.(*pgxpool.Pool)
	return &AdminHandler{db: db, pool: pool, logger: logger}
}

// ListTenants GET /admin/tenants?status=active&page=1&page_size=20
func (h *AdminHandler) ListTenants(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var totalRow pgx.Row
	var rows pgx.Rows
	var err error

	if status != "" {
		totalRow = h.db.QueryRow(c.Request.Context(),
			"SELECT COUNT(*) FROM public.tenants WHERE deleted_at IS NULL AND status=$1", status)
		rows, err = h.db.Query(c.Request.Context(),
			"SELECT id, name, slug, plan, status, created_at FROM public.tenants WHERE deleted_at IS NULL AND status=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3",
			status, pageSize, offset)
	} else {
		totalRow = h.db.QueryRow(c.Request.Context(),
			"SELECT COUNT(*) FROM public.tenants WHERE deleted_at IS NULL")
		rows, err = h.db.Query(c.Request.Context(),
			"SELECT id, name, slug, plan, status, created_at FROM public.tenants WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2",
			pageSize, offset)
	}

	var total int
	if scanErr := totalRow.Scan(&total); scanErr != nil {
		h.logger.Error("count tenants failed", zap.Error(scanErr))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	if err != nil {
		h.logger.Error("list tenants failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	defer rows.Close()

	tenants := make([]model.TenantResponse, 0)
	for rows.Next() {
		var t model.TenantResponse
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &t.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "scan error"})
			return
		}
		tenants = append(tenants, t)
	}

	c.JSON(http.StatusOK, model.ListTenantsResponse{
		Tenants: tenants, Total: total, Page: page, PageSize: pageSize,
	})
}

// GetTenant GET /admin/tenants/:id
func (h *AdminHandler) GetTenant(c *gin.Context) {
	id := c.Param("id")
	var t model.TenantResponse
	err := h.db.QueryRow(c.Request.Context(),
		"SELECT id, name, slug, plan, status, created_at, deleted_at FROM public.tenants WHERE id=$1", id,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &t.CreatedAt, &t.DeletedAt)
	if err != nil {
		h.logger.Warn("tenant not found", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

// CreateTenant POST /admin/tenants
func (h *AdminHandler) CreateTenant(c *gin.Context) {
	var req model.CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	id := uuid.New().String()
	now := time.Now().UTC()
	_, err := h.db.Exec(c.Request.Context(),
		"INSERT INTO public.tenants(id, name, slug, plan, status, created_at) VALUES($1,$2,$3,$4,$5,$6)",
		id, req.Name, req.Slug, req.Plan, req.Status, now)
	if err != nil {
		h.logger.Error("create tenant failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "create failed"})
		return
	}

	if h.pool != nil {
		if err := tenantdb.ProvisionTenantSchema(c.Request.Context(), h.pool, id); err != nil {
			h.logger.Error("provision tenant schema failed", zap.String("tenant_id", id), zap.Error(err))
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "schema provision failed"})
			return
		}
	}

	c.JSON(http.StatusCreated, model.TenantResponse{
		ID: id, Name: req.Name, Slug: req.Slug,
		Plan: req.Plan, Status: req.Status, CreatedAt: now,
	})
}

// UpdateTenant PATCH /admin/tenants/:id
func (h *AdminHandler) UpdateTenant(c *gin.Context) {
	id := c.Param("id")
	var req model.UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	tag, err := h.db.Exec(c.Request.Context(),
		"UPDATE public.tenants SET plan=COALESCE(NULLIF($1,''), plan), status=COALESCE(NULLIF($2,''), status) WHERE id=$3 AND deleted_at IS NULL",
		req.Plan, req.Status, id)
	if err != nil {
		h.logger.Error("update tenant failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant updated"})
}

// DeleteTenant DELETE /admin/tenants/:id — soft delete
func (h *AdminHandler) DeleteTenant(c *gin.Context) {
	id := c.Param("id")
	now := time.Now().UTC()
	tag, err := h.db.Exec(c.Request.Context(),
		"UPDATE public.tenants SET deleted_at=$1 WHERE id=$2 AND deleted_at IS NULL", now, id)
	if err != nil {
		h.logger.Error("delete tenant failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "delete failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant deleted"})
}

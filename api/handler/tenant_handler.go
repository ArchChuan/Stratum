// Package handler implements HTTP API request handlers.

package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TenantHandler struct {
	db          PgxPool
	logger      *zap.Logger
	frontendURL string
}

func NewTenantHandler(db PgxPool, logger *zap.Logger, frontendURL string) *TenantHandler {
	return &TenantHandler{db: db, logger: logger, frontendURL: frontendURL}
}

// ListMembers GET /tenant/members?page=1&page_size=20
func (h *TenantHandler) ListMembers(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int
	if err := h.db.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM public.tenant_members WHERE tenant_id=$1", tenantID,
	).Scan(&total); err != nil {
		h.logger.Error("count members failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}

	rows, err := h.db.Query(c.Request.Context(),
		`SELECT tm.user_id, u.email, tm.role, tm.created_at
		 FROM public.tenant_members tm
		 JOIN public.users u ON u.id = tm.user_id
		 WHERE tm.tenant_id=$1
		 ORDER BY tm.created_at DESC LIMIT $2 OFFSET $3`,
		tenantID, pageSize, offset)
	if err != nil {
		h.logger.Error("list members failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	defer rows.Close()

	members := make([]model.MemberResponse, 0)
	for rows.Next() {
		var m model.MemberResponse
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.JoinedAt); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "scan error"})
			return
		}
		members = append(members, m)
	}
	c.JSON(http.StatusOK, model.ListMembersResponse{
		Members: members, Total: total, Page: page, PageSize: pageSize,
	})
}

// InviteMember POST /tenant/members/invite
func (h *TenantHandler) InviteMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}

	// only admin or owner may invite
	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "admin or owner role required"})
		return
	}

	var req model.InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "token generation failed"})
		return
	}
	rawToken := hex.EncodeToString(rawBytes)
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])

	invitationID := uuid.New().String()
	expiresAt := time.Now().UTC().Add(72 * time.Hour)

	_, err := h.db.Exec(c.Request.Context(),
		`INSERT INTO public.invitations(id, tenant_id, email, role, token_hash, expires_at, created_at)
		 VALUES($1, $2, $3, $4, $5, $6, $7)`,
		invitationID, tenantID, req.Email, req.Role, tokenHash, expiresAt, time.Now().UTC())
	if err != nil {
		h.logger.Error("insert invitation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "invitation creation failed"})
		return
	}

	invitationURL := fmt.Sprintf("%s/onboarding?invitation=%s", h.frontendURL, rawToken)
	c.JSON(http.StatusCreated, model.InviteMemberResponse{
		InvitationID:  invitationID,
		Email:         req.Email,
		Role:          req.Role,
		InvitationURL: invitationURL,
		ExpiresAt:     expiresAt,
	})
}

// UpdateMemberRole PATCH /tenant/members/:user_id/role
func (h *TenantHandler) UpdateMemberRole(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	userID := c.Param("user_id")
	var req model.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	tag, err := h.db.Exec(c.Request.Context(),
		"UPDATE public.tenant_members SET role=$1 WHERE tenant_id=$2 AND user_id=$3",
		req.Role, tenantID, userID)
	if err != nil {
		h.logger.Error("update member role failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role updated"})
}

// RemoveMember DELETE /tenant/members/:user_id
func (h *TenantHandler) RemoveMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	userID := c.Param("user_id")
	tag, err := h.db.Exec(c.Request.Context(),
		"DELETE FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2",
		tenantID, userID)
	if err != nil {
		h.logger.Error("remove member failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "remove failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

// GetSettings GET /tenant/settings
func (h *TenantHandler) GetSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	var settingsJSON []byte
	err := h.db.QueryRow(c.Request.Context(),
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
	).Scan(&settingsJSON)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	var settings map[string]interface{}
	if len(settingsJSON) > 0 {
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "settings parse error"})
			return
		}
	} else {
		settings = map[string]interface{}{}
	}
	c.JSON(http.StatusOK, model.SettingsResponse{TenantID: tenantID, Settings: settings})
}

// UpdateSettings PATCH /tenant/settings
func (h *TenantHandler) UpdateSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	var req model.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	settingsJSON, err := json.Marshal(req.Settings)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: "invalid settings"})
		return
	}
	tag, err := h.db.Exec(c.Request.Context(),
		"UPDATE public.tenants SET settings=$1 WHERE id=$2 AND deleted_at IS NULL",
		settingsJSON, tenantID)
	if err != nil {
		h.logger.Error("update settings failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}

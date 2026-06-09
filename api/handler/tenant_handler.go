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
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	pkgcrypto "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TenantHandler struct {
	db          PgxPool
	logger      *zap.Logger
	frontendURL string
	aesKey      [32]byte
	cache       *llmgateway.TenantGatewayCache
}

func NewTenantHandler(db PgxPool, logger *zap.Logger, frontendURL string, aesKey [32]byte, cache *llmgateway.TenantGatewayCache) *TenantHandler {
	return &TenantHandler{db: db, logger: logger, frontendURL: frontendURL, aesKey: aesKey, cache: cache}
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
		`SELECT tm.user_id, u.github_login, COALESCE(u.avatar_url, ''), tm.role, tm.joined_at
		 FROM public.tenant_members tm
		 JOIN public.users u ON u.id = tm.user_id
		 WHERE tm.tenant_id=$1
		 ORDER BY tm.joined_at DESC LIMIT $2 OFFSET $3`,
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
		if err := rows.Scan(&m.UserID, &m.GitHubLogin, &m.AvatarURL, &m.Role, &m.JoinedAt); err != nil {
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

	inviterID, _ := c.Get("auth.sub")
	inviterIDStr, _ := inviterID.(string)
	if inviterIDStr == "" {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "inviter identity missing"})
		return
	}

	invitationID := uuid.New().String()
	expiresAt := time.Now().UTC().Add(72 * time.Hour)

	_, err := h.db.Exec(c.Request.Context(),
		`INSERT INTO public.invitations(id, tenant_id, email, role, token_hash, expires_at, created_at, invited_by)
		 VALUES($1, $2, $3, $4, $5, $6, $7, $8)`,
		invitationID, tenantID, req.Email, req.Role, tokenHash, expiresAt, time.Now().UTC(), inviterIDStr)
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

	// only owner may change roles
	roleVal, _ := c.Get("auth.role")
	if roleVal != "owner" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "owner role required"})
		return
	}

	userID := c.Param("user_id")

	// prevent changing own role
	callerID, _ := c.Get("auth.sub")
	if callerID == userID {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "cannot change your own role"})
		return
	}

	// prevent changing owner's role
	var targetRole string
	if err := h.db.QueryRow(c.Request.Context(),
		"SELECT role FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2",
		tenantID, userID,
	).Scan(&targetRole); err != nil {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}
	if targetRole == "owner" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "cannot change owner's role"})
		return
	}

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

	// only owner or admin may remove members
	roleVal, _ := c.Get("auth.role")
	callerRole, _ := roleVal.(string)
	if callerRole != "owner" && callerRole != "admin" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "admin or owner role required"})
		return
	}

	userID := c.Param("user_id")

	// prevent self-removal
	callerID, _ := c.Get("auth.sub")
	if callerID == userID {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "cannot remove yourself"})
		return
	}

	// get target's current role
	var targetRole string
	if err := h.db.QueryRow(c.Request.Context(),
		"SELECT role FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2",
		tenantID, userID,
	).Scan(&targetRole); err != nil {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}

	// owner cannot be removed
	if targetRole == "owner" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "cannot remove owner"})
		return
	}

	// admin can only remove regular members, not other admins
	if callerRole == "admin" && targetRole == "admin" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "admin cannot remove another admin"})
		return
	}

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
	var tenantName string
	var settingsJSON []byte
	err := h.db.QueryRow(c.Request.Context(),
		"SELECT name, settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
	).Scan(&tenantName, &settingsJSON)
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
	if apiKeys, ok := settings["llm_api_keys"].(map[string]interface{}); ok {
		masked := make(map[string]interface{}, len(apiKeys))
		for provider, val := range apiKeys {
			if s, ok := val.(string); ok && s != "" {
				decrypted, err := pkgcrypto.Decrypt(h.aesKey, s)
				if err == nil {
					masked[provider] = maskAPIKey(decrypted)
				} else {
					masked[provider] = ""
				}
			} else {
				masked[provider] = ""
			}
		}
		settings["llm_api_keys"] = masked
	}
	c.JSON(http.StatusOK, model.SettingsResponse{TenantID: tenantID, TenantName: tenantName, Settings: settings})
}

// UpdateSettings PATCH /tenant/settings
func (h *TenantHandler) UpdateSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}

	roleVal, _ := c.Get("auth.role")
	roleStr, _ := roleVal.(string)
	if roleStr != "admin" && roleStr != "owner" {
		c.JSON(http.StatusForbidden, model.ErrorResponse{Code: 403, Message: "admin or owner role required"})
		return
	}

	var req model.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	if req.Name != "" {
		tag, err := h.db.Exec(c.Request.Context(),
			"UPDATE public.tenants SET name=$1, updated_at=now() WHERE id=$2 AND deleted_at IS NULL",
			req.Name, tenantID)
		if err != nil {
			h.logger.Error("update tenant name failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
			return
		}
		if tag.RowsAffected() == 0 {
			c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
			return
		}
	}

	if req.Settings != nil {
		var existingJSON []byte
		_ = h.db.QueryRow(c.Request.Context(),
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
		).Scan(&existingJSON)

		merged := map[string]interface{}{}
		if len(existingJSON) > 0 {
			_ = json.Unmarshal(existingJSON, &merged)
		}

		if apiKeys, ok := req.Settings["llm_api_keys"].(map[string]interface{}); ok {
			encrypted := make(map[string]interface{}, len(apiKeys))
			for provider, val := range apiKeys {
				plaintext, ok := val.(string)
				if !ok || plaintext == "" {
					continue
				}
				// skip placeholder values sent back by the frontend (all bullet chars)
				if strings.Trim(plaintext, "•") == "" {
					continue
				}
				enc, err := pkgcrypto.Encrypt(h.aesKey, plaintext)
				if err != nil {
					h.logger.Error("encrypt api key failed", zap.String("provider", provider), zap.Error(err))
					c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "encryption failed"})
					return
				}
				encrypted[provider] = enc
			}
			existing, _ := merged["llm_api_keys"].(map[string]interface{})
			if existing == nil {
				existing = map[string]interface{}{}
			}
			for k, v := range encrypted {
				existing[k] = v
			}
			merged["llm_api_keys"] = existing
		}

		for k, v := range req.Settings {
			if k == "llm_api_keys" {
				continue
			}
			merged[k] = v
		}

		settingsJSON, err := json.Marshal(merged)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: "invalid settings"})
			return
		}
		if _, err := h.db.Exec(c.Request.Context(),
			"UPDATE public.tenants SET settings=$1, updated_at=now() WHERE id=$2 AND deleted_at IS NULL",
			settingsJSON, tenantID); err != nil {
			h.logger.Error("update settings failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
			return
		}

		if h.cache != nil {
			h.cache.Invalidate(tenantID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}

// ListUserTenants returns all tenants the current user belongs to.
// GET /tenant/list
func (h *TenantHandler) ListUserTenants(c *gin.Context) {
	userID, ok := c.Get("auth.sub")
	if !ok || userID == "" {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "unauthorized"})
		return
	}

	rows, err := h.db.Query(c.Request.Context(),
		`SELECT t.id, t.name, t.is_default
		 FROM tenant_members tm
		 JOIN tenants t ON t.id = tm.tenant_id
		 WHERE tm.user_id = $1 AND t.deleted_at IS NULL
		 ORDER BY t.is_default DESC, t.created_at ASC`,
		userID,
	)
	if err != nil {
		h.logger.Error("list user tenants", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	defer rows.Close()

	var items []model.TenantListItem
	for rows.Next() {
		var item model.TenantListItem
		if err := rows.Scan(&item.TenantID, &item.Name, &item.IsDefault); err != nil {
			h.logger.Error("scan tenant row", zap.Error(err))
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "scan error"})
			return
		}
		items = append(items, item)
	}
	if items == nil {
		items = []model.TenantListItem{}
	}
	c.JSON(http.StatusOK, model.TenantListResponse{Tenants: items})
}

// maskAPIKey shows the first 6 chars then 8 bullets — enough to identify the key without exposing it.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	show := 6
	if len(runes) <= show {
		show = len(runes) / 2
	}
	return string(runes[:show]) + strings.Repeat("•", 8)
}

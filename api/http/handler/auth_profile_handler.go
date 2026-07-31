package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/pkg/storage/filestore"
	"github.com/gin-gonic/gin"
)

type updateProfileReq struct {
	DisplayName string `json:"display_name"`
}

// UpdateProfile handles PATCH /auth/me.
func (h *AuthHandler) UpdateProfile(c *gin.Context) {
	var req updateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if req.DisplayName == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("display_name is required")))
		return
	}

	userID := h.userIDFromAuth(c)
	if userID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("authentication required")))
		return
	}

	if err := h.deps.OnboardSvc.UpdateProfile(c.Request.Context(), userID, req.DisplayName, ""); err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"display_name": req.DisplayName})
}

// UploadAvatar handles POST /auth/me/avatar (multipart).
func (h *AuthHandler) UploadAvatar(c *gin.Context) {
	if h.deps.AvatarStore == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("avatar storage not configured")))
		return
	}

	userID := h.userIDFromAuth(c)
	if userID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("authentication required")))
		return
	}

	fh, err := c.FormFile("avatar")
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("avatar file is required")))
		return
	}

	filename, err := h.deps.AvatarStore.SaveAvatarMultipart(fh, userID)
	if err != nil {
		if errors.Is(err, filestore.ErrAvatarTooLarge) || errors.Is(err, filestore.ErrAvatarInvalidExt) {
			_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
			return
		}
		_ = c.Error(err)
		return
	}

	avatarURL := filestore.URL(filename)
	if err := h.deps.OnboardSvc.UpdateProfile(c.Request.Context(), userID, "", avatarURL); err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"avatar_url": avatarURL})
}

// userIDFromAuth extracts the user ID from the Authorization header by verifying
// the JWT through the handler's JWT service.
func (h *AuthHandler) userIDFromAuth(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	if h.deps.JWTService == nil {
		return ""
	}
	claims, err := h.deps.JWTService.Verify(tokenStr)
	if err != nil || claims == nil {
		return ""
	}
	return claims.Sub
}

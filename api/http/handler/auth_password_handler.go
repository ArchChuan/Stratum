package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/crypto"
)

const (
	usernameMinLen = 3
	usernameMaxLen = 32
)

var validUsernameChars = func(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// UsernameRegister handles local username+password registration.
// POST /auth/password/register
func (h *AuthHandler) UsernameRegister(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("username and password required")))
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if err := validateUsername(req.Username); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := crypto.ValidatePassword(req.Password); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}

	hash, err := crypto.HashPassword(req.Password)
	if err != nil {
		h.deps.Logger.Error("hash password", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("password processing failed")))
		return
	}

	userID, tenantID, err := h.deps.OnboardSvc.RegisterByUsername(ctx, req.Username, hash)
	if err != nil {
		if errors.Is(err, domain.ErrUsernameTaken) {
			_ = c.Error(middleware.NewHTTPError(http.StatusConflict, err))
			return
		}
		h.deps.Logger.Error("register user", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("registration failed")))
		return
	}

	systemRole := domain.DeriveSystemRole([]domain.TenantMembership{
		{TenantID: tenantID, Role: "member"},
	})
	rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, tenantID, "member", "", systemRole, "", req.Username)
	if err != nil {
		h.deps.Logger.Error("issue token pair for register", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
		return
	}
	h.setRefreshCookie(c, rawRT)
	h.deps.Logger.Info("user registered", zap.String("username", req.Username), zap.String("user_id", userID))
	c.JSON(http.StatusCreated, gin.H{"access_token": accessJWT, "tenant_id": tenantID})
}

// UsernameLogin handles local username+password login.
// POST /auth/password/login
func (h *AuthHandler) UsernameLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("username and password required")))
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, errors.New("username required")))
		return
	}

	userID, passwordHash, githubLogin, globalRole, found, err := h.deps.OnboardSvc.FindByUsernameWithLogin(ctx, req.Username)
	if err != nil {
		h.deps.Logger.Error("find user", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("login failed")))
		return
	}
	if !found || passwordHash == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid username or password")))
		return
	}
	if !crypto.CheckPassword(req.Password, passwordHash) {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("invalid username or password")))
		return
	}

	// Look up user's default tenant by UUID.
	tid, role, lookupErr := h.deps.OnboardSvc.GetUserTenantByUserID(ctx, userID)
	if lookupErr != nil {
		h.deps.Logger.Error("get user tenant", zap.String("user_id", userID), zap.Error(lookupErr))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("account not fully provisioned")))
		return
	}

	systemRole := domain.DeriveSystemRole([]domain.TenantMembership{
		{TenantID: tid, Role: role},
	})
	rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, tid, role, globalRole, systemRole, "", githubLogin)
	if err != nil {
		h.deps.Logger.Error("issue token pair for login", zap.Error(err))
		_ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("token issuance failed")))
		return
	}
	h.setRefreshCookie(c, rawRT)
	h.deps.Logger.Info("user logged in", zap.String("username", req.Username), zap.String("user_id", userID))
	c.JSON(http.StatusOK, gin.H{"access_token": accessJWT, "tenant_id": tid})
}

func validateUsername(username string) error {
	if len(username) < usernameMinLen || len(username) > usernameMaxLen {
		return errors.New("username: 3-32 characters, letters, digits and underscore only")
	}
	for _, r := range username {
		if !validUsernameChars(r) {
			return errors.New("username: only letters, digits and underscore allowed")
		}
	}
	return nil
}

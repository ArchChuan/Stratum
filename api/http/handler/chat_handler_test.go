package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// mockChatStore is a test double for agent.ChatStore.
type mockChatStore struct {
	createFn    func(ctx context.Context, tenantID, agentID, userID, name string) (*agent.ChatConversation, error)
	listConvsFn func(ctx context.Context, tenantID, agentID, userID string) ([]*agent.ChatConversation, error)
	renameFn    func(ctx context.Context, tenantID, convID, userID, name string) error
	deleteFn    func(ctx context.Context, tenantID, convID, userID string) error
	addMsgFn    func(ctx context.Context, tenantID string, msg *agent.ChatMessage) error
	listMsgsFn  func(ctx context.Context, tenantID, convID, userID string) ([]*agent.ChatMessage, error)
	cleanupFn   func(ctx context.Context, tenantID string) error
}

func (m *mockChatStore) CreateConversation(ctx context.Context, tenantID, agentID, userID, name string) (*agent.ChatConversation, error) {
	return m.createFn(ctx, tenantID, agentID, userID, name)
}
func (m *mockChatStore) ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*agent.ChatConversation, error) {
	return m.listConvsFn(ctx, tenantID, agentID, userID)
}
func (m *mockChatStore) RenameConversation(ctx context.Context, tenantID, convID, userID, name string) error {
	return m.renameFn(ctx, tenantID, convID, userID, name)
}
func (m *mockChatStore) DeleteConversation(ctx context.Context, tenantID, convID, userID string) error {
	return m.deleteFn(ctx, tenantID, convID, userID)
}
func (m *mockChatStore) AddMessage(ctx context.Context, tenantID string, msg *agent.ChatMessage) error {
	return m.addMsgFn(ctx, tenantID, msg)
}
func (m *mockChatStore) ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*agent.ChatMessage, error) {
	return m.listMsgsFn(ctx, tenantID, convID, userID)
}
func (m *mockChatStore) CleanupExpired(ctx context.Context, tenantID string) error {
	return m.cleanupFn(ctx, tenantID)
}

func setupChatRouter(h *ChatHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	mid := func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: "t1", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		ctx = reqctx.WithTenantID(ctx, "t1")
		c.Request = c.Request.WithContext(ctx)
		c.Set(middleware.ContextKeySub, "u1")
		c.Next()
	}
	r.Use(mid)
	r.POST("/agents/:id/conversations", h.CreateConversation)
	r.GET("/agents/:id/conversations", h.ListConversations)
	r.PATCH("/conversations/:convID", h.RenameConversation)
	r.DELETE("/conversations/:convID", h.DeleteConversation)
	r.GET("/conversations/:convID/messages", h.ListMessages)
	r.POST("/conversations/:convID/messages", h.AddMessage)
	return r
}

func nowConv(id, name string) *agent.ChatConversation {
	now := time.Now()
	return &agent.ChatConversation{ID: id, AgentID: "a1", UserID: "u1", Name: name, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.AddDate(0, 0, 30)}
}

func nowMsg(id, role, content string) *agent.ChatMessage {
	return &agent.ChatMessage{ID: id, ConversationID: "c1", Role: role, Content: content, StepsJSON: []byte("[]"), CreatedAt: time.Now()}
}

// --- CreateConversation ---

func TestChatHandler_CreateConversation_success(t *testing.T) {
	store := &mockChatStore{
		createFn: func(_ context.Context, tenantID, agentID, userID, name string) (*agent.ChatConversation, error) {
			if tenantID != "t1" || agentID != "a1" || userID != "u1" {
				t.Errorf("unexpected args: %s %s %s", tenantID, agentID, userID)
			}
			return nowConv("c-new", name), nil
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	body := `{"name":"我的会话"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/agents/a1/conversations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["id"] != "c-new" {
		t.Errorf("want c-new, got %v", resp["id"])
	}
}

func TestChatHandler_CreateConversation_defaultName(t *testing.T) {
	store := &mockChatStore{
		createFn: func(_ context.Context, _, _, _, name string) (*agent.ChatConversation, error) {
			if name != "新会话" {
				t.Errorf("want default name 新会话, got %s", name)
			}
			return nowConv("c1", name), nil
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/agents/a1/conversations", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", w.Code)
	}
}

// --- ListConversations ---

func TestChatHandler_ListConversations_success(t *testing.T) {
	store := &mockChatStore{
		listConvsFn: func(_ context.Context, _, _, _ string) ([]*agent.ChatConversation, error) {
			return []*agent.ChatConversation{nowConv("c1", "A"), nowConv("c2", "B")}, nil
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/agents/a1/conversations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	convs := resp["conversations"].([]any)
	if len(convs) != 2 {
		t.Errorf("want 2 conversations, got %d", len(convs))
	}
}

// --- RenameConversation ---

func TestChatHandler_RenameConversation_success(t *testing.T) {
	store := &mockChatStore{
		renameFn: func(_ context.Context, _, _, _, name string) error {
			if name != "新名字" {
				t.Errorf("want 新名字, got %s", name)
			}
			return nil
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	body := `{"name":"新名字"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/conversations/c1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body)
	}
}

func TestChatHandler_RenameConversation_notFound(t *testing.T) {
	store := &mockChatStore{
		renameFn: func(_ context.Context, _, _, _, _ string) error { return agent.ErrNotFound },
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	body := `{"name":"x"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/conversations/no-such", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestChatHandler_RenameConversation_missingName(t *testing.T) {
	store := &mockChatStore{}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/conversations/c1", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// --- DeleteConversation ---

func TestChatHandler_DeleteConversation_success(t *testing.T) {
	store := &mockChatStore{
		deleteFn: func(_ context.Context, _, _, _ string) error { return nil },
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/conversations/c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
}

func TestChatHandler_DeleteConversation_notOwned(t *testing.T) {
	store := &mockChatStore{
		deleteFn: func(_ context.Context, _, _, _ string) error { return agent.ErrNotFound },
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/conversations/c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// --- ListMessages ---

func TestChatHandler_ListMessages_success(t *testing.T) {
	store := &mockChatStore{
		listMsgsFn: func(_ context.Context, _, _, _ string) ([]*agent.ChatMessage, error) {
			return []*agent.ChatMessage{nowMsg("m1", "user", "hi"), nowMsg("m2", "agent", "hello")}, nil
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/conversations/c1/messages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	msgs := resp["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("want 2 messages, got %d", len(msgs))
	}
}

// --- AddMessage ---

func TestChatHandler_AddMessage_success(t *testing.T) {
	store := &mockChatStore{
		// ListMessages used for ownership check: return empty, no error = owned
		listMsgsFn: func(_ context.Context, _, _, _ string) ([]*agent.ChatMessage, error) {
			return nil, nil
		},
		addMsgFn: func(_ context.Context, _ string, msg *agent.ChatMessage) error {
			msg.ID = "m-new"
			msg.CreatedAt = time.Now()
			return nil
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	body := `{"role":"user","content":"test message"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/conversations/c1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["id"] != "m-new" {
		t.Errorf("want m-new, got %v", resp["id"])
	}
}

func TestChatHandler_AddMessage_invalidRole(t *testing.T) {
	store := &mockChatStore{}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	body := `{"role":"admin","content":"x"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/conversations/c1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestChatHandler_AddMessage_ownershipFailed(t *testing.T) {
	store := &mockChatStore{
		listMsgsFn: func(_ context.Context, _, _, _ string) ([]*agent.ChatMessage, error) {
			return nil, errors.New("no rows or forbidden")
		},
	}
	h := NewChatHandler(store, zap.NewNop())
	r := setupChatRouter(h)

	body := `{"role":"user","content":"x"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/conversations/c1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

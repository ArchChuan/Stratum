package testserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type Tool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type Behavior struct {
	Result        map[string]any
	Delay         time.Duration
	ProtocolError bool
	Disconnect    bool
	HalfResponse  bool
}

type Server struct {
	server *httptest.Server
	mu     sync.RWMutex
	tools  []Tool
	byName map[string]Behavior
	calls  map[string]int
	digest map[string]string
}

func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		byName: make(map[string]Behavior),
		calls:  make(map[string]int),
		digest: make(map[string]string),
	}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) URL() string { return s.server.URL }
func (s *Server) Close()      { s.server.Close() }

func (s *Server) SetTools(tools []Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append([]Tool(nil), tools...)
}

func (s *Server) SetBehavior(toolName string, behavior Behavior) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byName[toolName] = behavior
}

func (s *Server) Attempts(toolName string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.calls[toolName]
}

func (s *Server) LastArgumentsDigest(toolName string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.digest[toolName]
}

func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = make(map[string]int)
	s.digest = make(map[string]string)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeRPCError(w, request.ID, -32700)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch request.Method {
	case "initialize":
		writeRPCResult(w, request.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "stratum-fake-mcp", "version": "1.0"},
		})
	case "tools/list":
		s.mu.RLock()
		tools := append([]Tool(nil), s.tools...)
		s.mu.RUnlock()
		writeRPCResult(w, request.ID, map[string]any{"tools": tools})
	case "tools/call":
		s.callTool(r.Context(), w, request)
	default:
		writeRPCError(w, request.ID, -32601)
	}
}

func (s *Server) callTool(ctx context.Context, w http.ResponseWriter, request rpcRequest) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		writeRPCError(w, request.ID, -32602)
		return
	}
	raw, _ := json.Marshal(params.Arguments)
	sum := sha256.Sum256(raw)
	s.mu.Lock()
	s.calls[params.Name]++
	s.digest[params.Name] = "sha256:" + hex.EncodeToString(sum[:])
	behavior, ok := s.byName[params.Name]
	s.mu.Unlock()
	if !ok {
		writeRPCError(w, request.ID, -32602)
		return
	}
	if behavior.Delay > 0 {
		timer := time.NewTimer(behavior.Delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
	}
	if behavior.Disconnect || behavior.HalfResponse {
		disconnectHTTP(w, behavior.HalfResponse)
		return
	}
	if behavior.ProtocolError {
		writeRPCError(w, request.ID, -32000)
		return
	}
	result := behavior.Result
	if result == nil {
		result = map[string]any{"content": []any{}}
	}
	writeRPCResult(w, request.ID, result)
}

func disconnectHTTP(w http.ResponseWriter, halfResponse bool) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, buffer, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if halfResponse {
		_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"jsonrpc\":\"2.0\"")
		_ = buffer.Flush()
	}
	_ = conn.Close()
}

func writeRPCResult(w http.ResponseWriter, id, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id any, code int) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": "fake MCP protocol error"},
	})
}

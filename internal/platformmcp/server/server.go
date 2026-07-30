package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/platformmcp/requestctx"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"go.uber.org/zap"
)

const protocolVersion = "2025-06-18"

type Dispatcher interface {
	ListTools(context.Context) []mcpdomain.Tool
	CallTool(context.Context, string, map[string]any) (agentport.MCPToolResult, error)
}

type ReadinessChecker interface {
	Check(context.Context) error
}

type Metrics interface {
	IncPlatformMCPRequest(toolClass, riskLevel, outcome string)
	RecordPlatformMCPRequestDuration(toolClass, outcome string, duration float64)
	IncPlatformMCPRequestsInFlight()
	DecPlatformMCPRequestsInFlight()
	IncPlatformMCPAuthDenial(statusClass string)
}

type Config struct {
	Dispatcher     Dispatcher
	Readiness      ReadinessChecker
	Logger         *zap.Logger
	Metrics        Metrics
	MetricsHandler http.Handler
}

type Server struct {
	dispatcher Dispatcher
	readiness  ReadinessChecker
	logger     *zap.Logger
	metrics    Metrics
	handler    http.Handler
}

func New(cfg Config) (*Server, error) {
	if cfg.Dispatcher == nil {
		return nil, errors.New("platform MCP dispatcher is not configured")
	}
	if cfg.Readiness == nil {
		return nil, errors.New("platform MCP readiness checker is not configured")
	}
	if cfg.Logger == nil {
		return nil, errors.New("platform MCP logger is not configured")
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = noopMetrics{}
	}
	server := &Server{dispatcher: cfg.Dispatcher, readiness: cfg.Readiness, logger: cfg.Logger, metrics: metrics}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", server.serveMCP)
	mux.HandleFunc("GET /healthz", server.serveHealth)
	mux.HandleFunc("GET /readyz", server.serveReady)
	if cfg.MetricsHandler != nil {
		mux.Handle("GET /metrics", cfg.MetricsHandler)
	}
	server.handler = mux
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) serveMCP(w http.ResponseWriter, req *http.Request) {
	request, err := decodeRPCRequest(w, req)
	if err != nil {
		writeRPCError(w, nil, rpcInvalidRequest, "invalid request")
		return
	}
	switch request.Method {
	case "initialize":
		s.initialize(w, request)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		s.listTools(req.Context(), w, request)
	case "resources/list":
		s.listResources(w, request)
	case "tools/call":
		callCtx, ok := invocationRequestContext(req)
		if !ok {
			s.metrics.IncPlatformMCPAuthDenial("4xx")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing invocation credential"})
			return
		}
		s.callTool(callCtx, w, request)
	default:
		writeRPCError(w, request.ID, rpcMethodNotFound, "method not found")
	}
}

func invocationRequestContext(req *http.Request) (context.Context, bool) {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return nil, false
	}
	return requestctx.WithInvocationToken(req.Context(), token), true
}

func (s *Server) initialize(w http.ResponseWriter, request rpcRequest) {
	var params initializeParams
	if err := decodeStrict(request.Params, &params); err != nil || params.ProtocolVersion != protocolVersion {
		writeRPCError(w, request.ID, rpcInvalidParams, "invalid initialize parameters")
		return
	}
	writeRPCResult(w, request.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false, "subscribe": false},
		},
		"serverInfo": map[string]string{"name": "stratum-platform-mcp", "version": "1.0"},
	})
}

func (s *Server) listTools(ctx context.Context, w http.ResponseWriter, request rpcRequest) {
	var params listToolsParams
	if err := decodeStrict(request.Params, &params); err != nil {
		writeRPCError(w, request.ID, rpcInvalidParams, "invalid tools/list parameters")
		return
	}
	writeRPCResult(w, request.ID, map[string]any{"tools": s.dispatcher.ListTools(ctx)})
}

func (s *Server) listResources(w http.ResponseWriter, request rpcRequest) {
	var params listToolsParams
	if err := decodeStrict(request.Params, &params); err != nil {
		writeRPCError(w, request.ID, rpcInvalidParams, "invalid resources/list parameters")
		return
	}
	writeRPCResult(w, request.ID, map[string]any{"resources": []any{}})
}

func (s *Server) callTool(ctx context.Context, w http.ResponseWriter, request rpcRequest) {
	var params callToolParams
	if err := decodeStrict(request.Params, &params); err != nil || params.Name == "" {
		writeRPCError(w, request.ID, rpcInvalidParams, "invalid tools/call parameters")
		return
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	toolClass, riskLevel := classifyTool(params.Name)
	started := time.Now()
	s.metrics.IncPlatformMCPRequestsInFlight()
	defer s.metrics.DecPlatformMCPRequestsInFlight()
	result, err := s.dispatcher.CallTool(ctx, params.Name, params.Arguments)
	outcome := "success"
	if err != nil {
		outcome = "definite_failure"
		s.logger.Warn("platform_mcp.tool.failed",
			zap.String("tool", params.Name),
			zap.String("outcome", "definite_failure"),
		)
		result = agentport.MCPToolResult{
			Content: []agentport.MCPContent{{Type: "text", Text: "工具调用失败"}},
			IsError: true,
		}
	}
	s.metrics.IncPlatformMCPRequest(toolClass, riskLevel, outcome)
	s.metrics.RecordPlatformMCPRequestDuration(toolClass, outcome, time.Since(started).Seconds())
	writeRPCResult(w, request.ID, result)
}

func classifyTool(name string) (string, string) {
	switch name {
	case platformmcp.ToolSearchOfficialDocs:
		return "docs", "read"
	case platformmcp.ToolDiagnoseTenant:
		return "diagnostic", "read"
	case platformmcp.ToolProposeResourceChange:
		return "proposal", "write_reversible"
	default:
		return "unknown", "unclassified"
	}
}

func (s *Server) serveHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) serveReady(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), constants.RouterHealthTimeout)
	defer cancel()
	if err := s.readiness.Check(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      clientInfo     `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type listToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

const (
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

func decodeRPCRequest(w http.ResponseWriter, req *http.Request) (rpcRequest, error) {
	req.Body = http.MaxBytesReader(w, req.Body, constants.MaxRequestBodyBytes)
	defer req.Body.Close()
	var request rpcRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return rpcRequest{}, fmt.Errorf("decode JSON-RPC request: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return rpcRequest{}, err
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return rpcRequest{}, errors.New("invalid JSON-RPC envelope")
	}
	return request, nil
}

func decodeStrict(data []byte, target any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON-RPC parameters: %w", err)
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("JSON value has trailing content")
	}
	return nil
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type noopMetrics struct{}

func (noopMetrics) IncPlatformMCPRequest(_, _, _ string)                    {}
func (noopMetrics) RecordPlatformMCPRequestDuration(_, _ string, _ float64) {}
func (noopMetrics) IncPlatformMCPRequestsInFlight()                         {}
func (noopMetrics) DecPlatformMCPRequestsInFlight()                         {}
func (noopMetrics) IncPlatformMCPAuthDenial(_ string)                       {}

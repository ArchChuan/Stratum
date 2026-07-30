package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	defaultListenAddress   = "127.0.0.1:18081"
	defaultExpectedTool    = "stratum_propose_resource_change"
	stubModel              = "platform-assistant-stub"
	maxRequestBodyBytes    = 64 * 1024
	maxCallsResponseBytes  = 1024
	maxObservedCallCounter = 1_000_000
)

var expectedToolPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type config struct {
	listenAddress string
	expectedTool  string
}

func parseConfig(args []string) (config, error) {
	cfg := config{}
	flags := flag.NewFlagSet("platform-assistant-stubs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.listenAddress, "listen-address", defaultListenAddress, "IPv4 loopback listen address")
	flags.StringVar(&cfg.listenAddress, "listen", defaultListenAddress, "alias for -listen-address")
	flags.StringVar(&cfg.expectedTool, "expected-tool", defaultExpectedTool, "tool emitted by the model stub")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}
	if err := validateListenAddress(cfg.listenAddress); err != nil {
		return config{}, err
	}
	if !expectedToolPattern.MatchString(cfg.expectedTool) {
		return config{}, errors.New("expected tool name is invalid")
	}
	return cfg, nil
}

func validateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("listen address must be host:port")
	}
	if host != "127.0.0.1" {
		return errors.New("listen address must use 127.0.0.1")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return errors.New("listen port is invalid")
	}
	return nil
}

type boundedCounter struct {
	value atomic.Uint64
}

func (c *boundedCounter) increment() uint64 {
	for {
		current := c.value.Load()
		if current >= maxObservedCallCounter {
			return uint64(maxObservedCallCounter)
		}
		if c.value.CompareAndSwap(current, current+1) {
			return current + 1
		}
	}
}

func (c *boundedCounter) load() uint64 {
	return min(c.value.Load(), uint64(maxObservedCallCounter))
}

type callCounters struct {
	Ready            uint64 `json:"ready"`
	Models           uint64 `json:"models"`
	ChatCompletions  uint64 `json:"chatCompletions"`
	MCPInitialize    uint64 `json:"mcpInitialize"`
	MCPToolsList     uint64 `json:"mcpToolsList"`
	MCPResourcesList uint64 `json:"mcpResourcesList"`
}

type stubServer struct {
	expectedTool string
	ready        boundedCounter
	models       boundedCounter
	chat         boundedCounter
	proposals    boundedCounter
	initialize   boundedCounter
	toolsList    boundedCounter
	resources    boundedCounter
}

func newStub(expectedTool string) http.Handler {
	stub := &stubServer{expectedTool: expectedTool}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", stub.handleReady)
	mux.HandleFunc("GET /calls", stub.handleCalls)
	mux.HandleFunc("GET /v1/models", stub.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", stub.handleChatCompletions)
	mux.HandleFunc("POST /mcp", stub.handleMCP)
	mux.HandleFunc("POST /", stub.handleMCP)
	return mux
}

func (s *stubServer) handleReady(w http.ResponseWriter, _ *http.Request) {
	s.ready.increment()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *stubServer) handleCalls(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, callCounters{
		Ready: s.ready.load(), Models: s.models.load(), ChatCompletions: s.chat.load(),
		MCPInitialize: s.initialize.load(), MCPToolsList: s.toolsList.load(),
		MCPResourcesList: s.resources.load(),
	})
}

func (s *stubServer) handleModels(w http.ResponseWriter, _ *http.Request) {
	s.models.increment()
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []any{map[string]any{
			"id": stubModel, "object": "model", "created": 0, "owned_by": "stratum-e2e",
		}},
	})
}

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role string `json:"role"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func (s *stubServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.chat.increment()
	var request chatRequest
	if err := decodeBoundedJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	model := request.Model
	if model == "" {
		model = stubModel
	}
	hasExpectedTool := false
	for _, tool := range request.Tools {
		if tool.Function.Name == s.expectedTool {
			hasExpectedTool = true
			break
		}
	}
	if len(request.Tools) == 0 {
		s.writeCompletion(w, request.Stream, model, false, "ok")
		return
	}
	if !hasExpectedTool {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected tool unavailable"})
		return
	}
	for _, message := range request.Messages {
		if message.Role == "tool" {
			s.writeCompletion(w, request.Stream, model, false,
				"Knowledge workspace proposal is ready for review.")
			return
		}
	}
	s.writeCompletion(w, request.Stream, model, true, "", s.proposals.increment())
}

func (s *stubServer) writeCompletion(
	w http.ResponseWriter,
	stream bool,
	model string,
	toolCall bool,
	content string,
	proposalSequence ...uint64,
) {
	sequence := uint64(0)
	if len(proposalSequence) > 0 {
		sequence = proposalSequence[0]
	}
	if stream {
		s.writeStreamCompletion(w, model, toolCall, content, sequence)
		return
	}
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if toolCall {
		message["content"] = nil
		message["tool_calls"] = []any{s.proposalToolCall(false, sequence)}
		finishReason = "tool_calls"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "chatcmpl-platform-assistant-stub", "object": "chat.completion", "created": 0, "model": model,
		"choices": []any{map[string]any{
			"index": 0, "message": message, "finish_reason": finishReason,
		}},
		"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func (s *stubServer) writeStreamCompletion(
	w http.ResponseWriter,
	model string,
	toolCall bool,
	content string,
	sequence uint64,
) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	delta := map[string]any{"role": "assistant"}
	finishReason := "stop"
	if toolCall {
		delta["tool_calls"] = []any{s.proposalToolCall(true, sequence)}
		finishReason = "tool_calls"
	} else {
		delta["content"] = content
	}
	writeSSE(w, map[string]any{
		"id": "chatcmpl-platform-assistant-stub", "object": "chat.completion.chunk", "created": 0, "model": model,
		"choices": []any{map[string]any{
			"index": 0, "delta": delta, "finish_reason": finishReason,
		}},
	})
	writeSSE(w, map[string]any{
		"id": "chatcmpl-platform-assistant-stub", "object": "chat.completion.chunk", "created": 0, "model": model,
		"choices": []any{},
		"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *stubServer) proposalToolCall(stream bool, sequence uint64) map[string]any {
	arguments := `{"resourceKind":"knowledge_workspace","operation":"create","payload":{` +
		`"name":"E2E Knowledge Workspace ` + strconv.FormatUint(sequence, 10) + `",` +
		`"description":"Created by the deterministic platform assistant E2E stub.",` +
		`"embeddingModel":"text-embedding-v3"}}`
	toolCall := map[string]any{
		"id": "call-platform-assistant-proposal-" + strconv.FormatUint(sequence, 10), "type": "function",
		"function": map[string]any{"name": s.expectedTool, "arguments": arguments},
	}
	if stream {
		toolCall["index"] = 0
	}
	return toolCall
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
}

func (s *stubServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	var request rpcRequest
	if err := decodeBoundedJSON(w, r, &request); err != nil || request.JSONRPC != "2.0" {
		writeRPCError(w, request.ID, -32700, "invalid JSON-RPC request")
		return
	}
	var result any
	switch request.Method {
	case "initialize":
		s.initialize.increment()
		result = map[string]any{
			"protocolVersion": constants.MCPProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false}, "resources": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]string{"name": "stratum-platform-assistant-e2e", "version": "1.0.0"},
		}
	case "tools/list":
		s.toolsList.increment()
		result = map[string]any{"tools": []any{map[string]any{
			"name": "read_verified_docs", "description": "Return deterministic verified documentation metadata.",
			"inputSchema": map[string]any{"type": "object", "additionalProperties": false},
		}}}
	case "resources/list":
		s.resources.increment()
		result = map[string]any{"resources": []any{}}
	default:
		writeRPCError(w, request.ID, -32601, "method not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, target any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func writeSSE(w http.ResponseWriter, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func run(args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return errors.New("invalid configuration")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.listenAddress)
	if err != nil {
		return errors.New("listen failed")
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           newStub(cfg.expectedTool),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			return errors.New("shutdown failed")
		}
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("server failed")
		}
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "platform-assistant-stubs:", err)
		os.Exit(1)
	}
}

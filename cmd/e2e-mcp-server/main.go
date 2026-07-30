package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	defaultListenAddress = "127.0.0.1:19091"
	readHeaderTimeout    = 5 * time.Second
	optimizationContent  = `[{"prompt_patch":{"instructions":"先分析 stateful 输入，再按明确步骤输出。"},"rationale":"提高可验证性"},{"prompt_patch":{"instructions":"对 stateful 输入给出简洁且可核对的结果。"},"rationale":"减少歧义"}]`
)

type rpcRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type opikEvidence struct {
	TraceID    string `json:"trace_id"`
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id"`
	ResourceID string `json:"resource_id"`
	RevisionID string `json:"revision_id"`
}

var opikEvidenceRegistry sync.Map
var contextEvidence = struct {
	sync.Mutex
	KnowledgeMarker string
	MemoryMarker    string
	KnowledgeSeen   bool
	MemorySeen      bool
}{}

func main() {
	address := os.Getenv("E2E_MCP_LISTEN_ADDRESS")
	if address == "" {
		address = defaultListenAddress
	}
	server := &http.Server{Addr: address, Handler: http.HandlerFunc(handler), ReadHeaderTimeout: readHeaderTimeout}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/e2e/context/register" || r.URL.Path == "/e2e/context/evidence" {
		contextEvidenceHandler(w, r)
		return
	}
	if r.URL.Path == "/e2e/opik/register" || r.URL.Path == "/opik/v1/private/traces" ||
		r.URL.Path == "/opik/v1/private/spans" {
		opikHandler(w, r)
		return
	}
	if r.URL.Path == "/v1/chat/completions" {
		completionHandler(w, r)
		return
	}
	if r.URL.Path == "/v1/embeddings" {
		embeddingsHandler(w, r)
		return
	}
	if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
		modelsHandler(w, r)
		return
	}
	mcpHandler(w, r)
}

func contextEvidenceHandler(w http.ResponseWriter, r *http.Request) {
	contextEvidence.Lock()
	defer contextEvidence.Unlock()
	if r.URL.Path == "/e2e/context/register" && r.Method == http.MethodPost {
		defer r.Body.Close()
		var request struct {
			KnowledgeMarker string `json:"knowledge_marker"`
			MemoryMarker    string `json:"memory_marker"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.KnowledgeMarker == "" || request.MemoryMarker == "" {
			http.Error(w, "invalid context marker registration", http.StatusBadRequest)
			return
		}
		contextEvidence.KnowledgeMarker, contextEvidence.MemoryMarker = request.KnowledgeMarker, request.MemoryMarker
		contextEvidence.KnowledgeSeen, contextEvidence.MemorySeen = false, false
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/e2e/context/evidence" && r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"knowledge_seen": contextEvidence.KnowledgeSeen, "memory_seen": contextEvidence.MemorySeen,
		})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func opikHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/e2e/opik/register" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var evidence opikEvidence
		if json.NewDecoder(r.Body).Decode(&evidence) != nil || evidence.TraceID == "" || evidence.TenantID == "" ||
			evidence.UserID == "" ||
			evidence.ResourceID == "" || evidence.RevisionID == "" {
			http.Error(w, "invalid evidence registration", http.StatusBadRequest)
			return
		}
		opikEvidenceRegistry.Store(evidence.TraceID, evidence)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/opik/v1/private/spans" {
		_ = json.NewEncoder(w).Encode(map[string]any{"page": 1, "size": 100, "total": 0, "content": []any{}})
		return
	}
	content := make([]any, 0)
	opikEvidenceRegistry.Range(func(_, value any) bool {
		evidence := value.(opikEvidence)
		manifest, _ := json.Marshal(map[string]string{"skill:" + evidence.ResourceID: evidence.RevisionID})
		content = append(content, map[string]any{
			"id": "opik-" + evidence.TraceID, "name": "agent.execute", "start_time": time.Now().UTC(),
			"duration": 1, "total_estimated_cost": 0,
			"usage": map[string]int{"total_tokens": 1},
			"metadata": map[string]any{
				"opik.metadata.stratum.tenant_id":         evidence.TenantID,
				"opik.metadata.stratum.user_id":           evidence.UserID,
				"opik.metadata.stratum.trace_id":          evidence.TraceID,
				"opik.metadata.stratum.execution_id":      "e2e-" + evidence.TraceID,
				"opik.metadata.stratum.status":            "success",
				"opik.metadata.stratum.resource_manifest": string(manifest),
			},
		})
		return true
	})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"page": 1, "size": len(content), "total": len(content), "content": content,
	})
}

func embeddingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var request struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Input) == 0 {
		http.Error(w, "invalid embedding request", http.StatusBadRequest)
		return
	}
	data := make([]any, len(request.Input))
	for index := range request.Input {
		embedding := make([]float64, 1024)
		embedding[0] = 1
		data[index] = map[string]any{"object": "embedding", "index": index, "embedding": embedding}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list", "model": request.Model, "data": data,
		"usage": map[string]any{"prompt_tokens": len(request.Input), "total_tokens": len(request.Input)},
	})
}

// modelsHandler returns a static model list compatible with the OpenAI /v1/models response
// format. This lets stateful E2E tests exercise real model discovery via the provider API.
func modelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "qwen-turbo", "object": "model"},
			{"id": "qwen-plus", "object": "model"},
			{"id": "qwen-max", "object": "model"},
			{"id": "text-embedding-v3", "object": "model"},
		},
	})
}

func completionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid completion request", http.StatusBadRequest)
		return
	}
	encodedMessages, _ := json.Marshal(request.Messages)
	contextEvidence.Lock()
	if contextEvidence.KnowledgeMarker != "" && strings.Contains(string(encodedMessages), contextEvidence.KnowledgeMarker) {
		contextEvidence.KnowledgeSeen = true
	}
	if contextEvidence.MemoryMarker != "" && strings.Contains(string(encodedMessages), contextEvidence.MemoryMarker) {
		contextEvidence.MemorySeen = true
	}
	contextEvidence.Unlock()
	model := request.Model
	if model == "" {
		model = "qwen-max"
	}
	calledTools := make(map[string]bool)
	for _, message := range request.Messages {
		for _, call := range message.ToolCalls {
			calledTools[call.Function.Name] = true
		}
	}
	toolName, toolArguments := nextToolCall(request.Tools, calledTools)
	wantsToolCall := toolName != ""
	if toolArguments == "" {
		toolArguments = `{"text":"stateful approval"}`
	}
	if request.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		if wantsToolCall {
			chunk := map[string]any{"model": model, "choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "stateful-tool-call", "type": "function",
					"function": map[string]any{"name": toolName, "arguments": toolArguments},
				}}}, "finish_reason": "tool_calls",
			}}}
			encoded, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(encoded) + "\n\n"))
		} else {
			_, _ = w.Write([]byte("data: {\"model\":\"" + model + "\",\"choices\":[{\"delta\":{\"content\":\"stateful stream completed\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":3,\"total_tokens\":7}}\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	content := "stateful sync completed"
	if request.Model == "qwen-plus" && len(request.Tools) == 0 {
		content = optimizationContent
	}
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if wantsToolCall {
		message = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
			"id": "stateful-tool-call", "type": "function",
			"function": map[string]any{"name": toolName, "arguments": toolArguments},
		}}}
		finishReason = "tool_calls"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model": model,
		"choices": []any{map[string]any{
			"finish_reason": finishReason,
			"message":       message,
		}},
		"usage": map[string]any{"prompt_tokens": 4, "completion_tokens": 3, "total_tokens": 7},
	})
}

func nextToolCall(tools []toolDef, called map[string]bool) (string, string) {
	contextEvidence.Lock()
	contextMode := contextEvidence.KnowledgeMarker != "" && contextEvidence.MemoryMarker != ""
	contextEvidence.Unlock()
	if contextMode {
		if name, args := findContextTool(tools, called); name != "" {
			return name, args
		}
	}
	if name, args := findSkillTool(tools, called); name != "" {
		return name, args
	}
	for _, tool := range tools {
		if len(tool.Function.Name) >= 4 && tool.Function.Name[:4] == "mcp:" && !called[tool.Function.Name] {
			return tool.Function.Name, `{"text":"stateful approval"}`
		}
	}
	return "", ""
}

type toolDef = struct {
	Function struct {
		Name       string         `json:"name"`
		Parameters map[string]any `json:"parameters"`
	} `json:"function"`
}

func findContextTool(tools []toolDef, called map[string]bool) (string, string) {
	for _, tool := range tools {
		name := tool.Function.Name
		if called[name] {
			continue
		}
		switch name {
		case "stratum_recall_memory":
			return name, `{"query":"历史偏好","limit":5}`
		case "stratum_search_knowledge":
			if args := buildKnowledgeArgs(tool); args != "" {
				return name, args
			}
		}
	}
	return "", ""
}

func buildKnowledgeArgs(tool toolDef) string {
	properties, _ := tool.Function.Parameters["properties"].(map[string]any)
	workspaces, _ := properties["workspaces"].(map[string]any)
	items, _ := workspaces["items"].(map[string]any)
	values, _ := items["enum"].([]any)
	if len(values) == 0 {
		return ""
	}
	workspace, ok := values[0].(string)
	if !ok {
		return ""
	}
	args, _ := json.Marshal(map[string]any{
		"workspaces": []string{workspace}, "query": "知识库上下文", "top_k": 5,
	})
	return string(args)
}

func findSkillTool(tools []toolDef, called map[string]bool) (string, string) {
	for _, tool := range tools {
		if tool.Function.Name != "stratum_activate_skill" || called[tool.Function.Name] {
			continue
		}
		properties, _ := tool.Function.Parameters["properties"].(map[string]any)
		skill, _ := properties["skill_id"].(map[string]any)
		values, _ := skill["enum"].([]any)
		if len(values) > 0 {
			if skillID, ok := values[0].(string); ok {
				arguments, _ := json.Marshal(map[string]string{"skill_id": skillID})
				return tool.Function.Name, string(arguments)
			}
		}
	}
	return "", ""
}

func mcpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var request rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, request.ID, -32700)
		return
	}
	var result any
	switch request.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": constants.MCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			"serverInfo":      map[string]any{"name": "stratum-stateful-mcp", "version": "1.0.0"},
		}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{
			"name": "stateful_echo", "description": "Return text for stateful acceptance",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		}}}
	case "tools/call":
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "stateful MCP call completed"}}}
	case "resources/list":
		result = map[string]any{"resources": []any{map[string]any{
			"uri": "stratum://stateful/evidence", "name": "Stateful evidence", "mimeType": "text/plain",
		}}}
	default:
		writeError(w, request.ID, -32601)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
}

func writeError(w http.ResponseWriter, id any, code int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": "stateful MCP protocol error"},
	})
}

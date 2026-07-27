package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPHandlerServesDeterministicProtocol(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"initialize", "tools/list", "tools/call", "resources/list"} {
		method := method
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": method,
				"params": map[string]any{"name": "stateful_echo", "arguments": map[string]any{"text": "probe"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			mcpHandler(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d", rec.Code)
			}
			var response struct {
				JSONRPC string          `json:"jsonrpc"`
				Result  json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.JSONRPC != "2.0" || len(response.Result) == 0 {
				t.Fatalf("response=%s", rec.Body.String())
			}
		})
	}
}

func TestHandlerServesOpenAICompatibleCompletion(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"qwen-max","messages":[{"role":"user","content":"stateful"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content == "" {
		t.Fatalf("response=%s", rec.Body.String())
	}
}

func TestCompletionReturnsOptimizationCandidatesForQwenPlus(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"qwen-plus","messages":[{"role":"system","content":"你是提示词优化器。只生成候选内容，不决定发布。仅输出 JSON 数组。"}]}`))
	rec := httptest.NewRecorder()
	completionHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("response=%s", rec.Body.String())
	}
	var candidates []struct {
		PromptPatch map[string]any `json:"prompt_patch"`
		Rationale   string         `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(response.Choices[0].Message.Content), &candidates); err != nil {
		t.Fatalf("optimization content is not JSON: %v content=%q", err, response.Choices[0].Message.Content)
	}
	if len(candidates) != 2 || candidates[0].PromptPatch["instructions"] == nil || candidates[1].Rationale == "" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
}

func TestCompletionAdvancesFromSkillActivationToMCPTool(t *testing.T) {
	t.Parallel()
	tools := `"tools":[` +
		`{"type":"function","function":{"name":"stratum_create_plan","parameters":{"type":"object"}}},` +
		`{"type":"function","function":{"name":"stratum_activate_skill","parameters":{"type":"object","properties":{"skill_id":{"type":"string","enum":["skill-1"]}}}}},` +
		`{"type":"function","function":{"name":"mcp:server-1:stateful_echo","parameters":{"type":"object"}}}]`

	activation := completionToolName(t, `{"messages":[{"role":"user"}],`+tools+`}`)
	if activation != "stratum_activate_skill" {
		t.Fatalf("first tool=%q", activation)
	}
	mcp := completionToolName(t, `{"messages":[{"role":"assistant","tool_calls":[{"function":{"name":"stratum_activate_skill"}}]},{"role":"tool"}],`+tools+`}`)
	if mcp != "mcp:server-1:stateful_echo" {
		t.Fatalf("second tool=%q", mcp)
	}
}

func TestHandlerServesOpenAICompatibleEmbeddings(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(
		`{"model":"text-embedding-v3","input":["first","second"]}`))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 2 {
		t.Fatalf("unexpected embedding item count: %d", len(response.Data))
	}
	if len(response.Data[0].Embedding) != 1024 || response.Data[1].Index != 1 {
		t.Fatalf("unexpected embedding response: dimensions=%d second_index=%d", len(response.Data[0].Embedding), response.Data[1].Index)
	}
}

func TestHandlerServesRegisteredOpikEvidence(t *testing.T) {
	t.Parallel()
	traceID := "trace-opik-contract"
	register := httptest.NewRequest(http.MethodPost, "/e2e/opik/register", bytes.NewBufferString(
		`{"trace_id":"`+traceID+`","tenant_id":"tenant-1","resource_id":"skill-1","revision_id":"revision-1"}`))
	registerRec := httptest.NewRecorder()
	handler(registerRec, register)
	if registerRec.Code != http.StatusNoContent {
		t.Fatalf("register status=%d body=%s", registerRec.Code, registerRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/opik/v1/private/traces?filters=ignored", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`skill:skill-1`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`revision-1`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(traceID)) {
		t.Fatalf("trace evidence=%s", rec.Body.String())
	}

	spans := httptest.NewRequest(http.MethodGet, "/opik/v1/private/spans?trace_id=opik-"+traceID, nil)
	spansRec := httptest.NewRecorder()
	handler(spansRec, spans)
	if spansRec.Code != http.StatusOK || !bytes.Contains(spansRec.Body.Bytes(), []byte(`"content":[]`)) {
		t.Fatalf("span evidence status=%d body=%s", spansRec.Code, spansRec.Body.String())
	}
}

func TestCompletionRecordsContextMarkersWithoutReturningPrompt(t *testing.T) {
	register := httptest.NewRequest(http.MethodPost, "/e2e/context/register", bytes.NewBufferString(
		`{"knowledge_marker":"knowledge-42","memory_marker":"memory-73"}`))
	registerRec := httptest.NewRecorder()
	handler(registerRec, register)
	if registerRec.Code != http.StatusNoContent {
		t.Fatalf("register status=%d", registerRec.Code)
	}
	completion := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"messages":[{"role":"system","content":"knowledge-42 and memory-73"}]}`))
	completionRec := httptest.NewRecorder()
	handler(completionRec, completion)

	evidence := httptest.NewRequest(http.MethodGet, "/e2e/context/evidence", nil)
	evidenceRec := httptest.NewRecorder()
	handler(evidenceRec, evidence)
	if evidenceRec.Code != http.StatusOK || evidenceRec.Body.String() != "{\"knowledge_seen\":true,\"memory_seen\":true}\n" {
		t.Fatalf("context evidence=%s", evidenceRec.Body.String())
	}
	if bytes.Contains(completionRec.Body.Bytes(), []byte("knowledge-42")) ||
		bytes.Contains(completionRec.Body.Bytes(), []byte("memory-73")) {
		t.Fatalf("completion exposed context markers: %s", completionRec.Body.String())
	}
}

func completionToolName(t *testing.T, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	completionHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("response=%s", rec.Body.String())
	}
	return response.Choices[0].Message.ToolCalls[0].Function.Name
}

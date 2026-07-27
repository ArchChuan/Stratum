package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testExpectedTool = "stratum_propose_resource_change"

func TestParseConfigRequiresIPv4Loopback(t *testing.T) {
	cfg, err := parseConfig([]string{"-listen-address", "127.0.0.1:18081", "-expected-tool", testExpectedTool})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.listenAddress != "127.0.0.1:18081" || cfg.expectedTool != testExpectedTool {
		t.Fatalf("config = %+v", cfg)
	}

	for _, address := range []string{"0.0.0.0:18081", "localhost:18081", "[::1]:18081", ":18081"} {
		t.Run(address, func(t *testing.T) {
			if _, err := parseConfig([]string{"-listen-address", address, "-expected-tool", testExpectedTool}); err == nil {
				t.Fatalf("parseConfig() accepted non-127.0.0.1 address %q", address)
			}
		})
	}
}

func TestChatCompletionsNonStreamingProposalThenFinal(t *testing.T) {
	server := httptest.NewServer(newStub(testExpectedTool))
	defer server.Close()

	first := postChat(t, server.URL, false, false)
	choice := first["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	toolCall := message["tool_calls"].([]any)[0].(map[string]any)
	function := toolCall["function"].(map[string]any)
	if function["name"] != testExpectedTool {
		t.Fatalf("tool name = %v", function["name"])
	}
	assertKnowledgeProposalArguments(t, function["arguments"].(string))

	second := postChat(t, server.URL, false, true)
	choice = second["choices"].([]any)[0].(map[string]any)
	message = choice["message"].(map[string]any)
	if content, _ := message["content"].(string); content == "" {
		t.Fatal("second response did not contain a final answer")
	}
}

func TestChatCompletionsStreamingProposalThenFinal(t *testing.T) {
	server := httptest.NewServer(newStub(testExpectedTool))
	defer server.Close()

	first := postStream(t, server.URL, false)
	if !strings.Contains(first, `"name":"`+testExpectedTool+`"`) || !strings.Contains(first, "data: [DONE]") {
		t.Fatalf("proposal stream = %q", first)
	}
	arguments := streamToolArguments(t, first)
	assertKnowledgeProposalArguments(t, arguments)

	second := postStream(t, server.URL, true)
	if !strings.Contains(second, `"content":"`) || !strings.Contains(second, "data: [DONE]") {
		t.Fatalf("final stream = %q", second)
	}
}

func TestReadyModelsMCPAndBoundedCallCounters(t *testing.T) {
	server := httptest.NewServer(newStub(testExpectedTool))
	defer server.Close()

	assertStatus(t, http.MethodGet, server.URL+"/readyz", nil, http.StatusOK)
	assertStatus(t, http.MethodGet, server.URL+"/v1/models", nil, http.StatusOK)
	for _, method := range []string{"initialize", "tools/list", "resources/list"} {
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		assertStatus(t, http.MethodPost, server.URL+"/mcp", body, http.StatusOK)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/calls", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer must-not-appear")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var counters callCounters
	if err := json.Unmarshal(raw, &counters); err != nil {
		t.Fatalf("decode counters: %v", err)
	}
	if counters.Ready != 1 || counters.Models != 1 || counters.MCPInitialize != 1 ||
		counters.MCPToolsList != 1 || counters.MCPResourcesList != 1 {
		t.Fatalf("counters = %+v", counters)
	}
	if bytes.Contains(raw, []byte("must-not-appear")) || len(raw) > maxCallsResponseBytes {
		t.Fatalf("unsafe or unbounded calls response: %q", raw)
	}
}

func postChat(t *testing.T, baseURL string, stream, includeToolResult bool) map[string]any {
	t.Helper()
	body := chatRequestBody(stream, includeToolResult)
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer must-not-be-recorded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d", response.StatusCode)
	}
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func postStream(t *testing.T, baseURL string, includeToolResult bool) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions",
		bytes.NewReader(chatRequestBody(true, includeToolResult)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func chatRequestBody(stream, includeToolResult bool) []byte {
	messages := []map[string]any{{"role": "user", "content": "Create a governed knowledge workspace proposal."}}
	if includeToolResult {
		messages = append(messages, map[string]any{
			"role": "tool", "tool_call_id": "call-proposal-1", "content": `{"status":"ready_for_review"}`,
		})
	}
	body, _ := json.Marshal(map[string]any{
		"model": "platform-assistant-stub", "stream": stream, "messages": messages,
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": testExpectedTool}}},
	})
	return body
}

func assertKnowledgeProposalArguments(t *testing.T, raw string) {
	t.Helper()
	var arguments map[string]any
	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		t.Fatalf("decode tool arguments: %v", err)
	}
	if arguments["resourceKind"] != "knowledge_workspace" || arguments["operation"] != "create" {
		t.Fatalf("proposal arguments = %v", arguments)
	}
	payload, ok := arguments["payload"].(map[string]any)
	if !ok || payload["name"] == "" || payload["description"] == "" || payload["embeddingModel"] != "text-embedding-v3" {
		t.Fatalf("proposal payload = %v", payload)
	}
}

func streamToolArguments(t *testing.T, stream string) string {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(stream))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || strings.HasSuffix(line, "[DONE]") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err == nil &&
			len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta.ToolCalls) > 0 {
			return chunk.Choices[0].Delta.ToolCalls[0].Function.Arguments
		}
	}
	t.Fatal("stream did not contain tool arguments")
	return ""
}

func assertStatus(t *testing.T, method, url string, body []byte, want int) {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("%s %s status = %d, want %d", method, url, response.StatusCode, want)
	}
}

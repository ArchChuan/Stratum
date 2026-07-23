//go:build ignore

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type completionRequest struct {
	Messages []struct {
		Role string `json:"role"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func main() {
	address := requiredEnv("E2E_LLM_ADDRESS")
	evidencePath := requiredEnv("E2E_LLM_EVIDENCE")
	embedEvidencePath := requiredEnv("E2E_EMBED_EVIDENCE")
	var requests atomic.Int64
	var embedRequests atomic.Int64
	var failing atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/mode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Failure bool `json:"failure"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			http.Error(w, "invalid mode", http.StatusBadRequest)
			return
		}
		failing.Store(body.Failure)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		count := requests.Add(1)
		_ = os.WriteFile(evidencePath, []byte("requests="+decimalString(count)+"\n"), 0o600)
		if failing.Load() {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		var request completionRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		toolResultSeen := false
		for _, message := range request.Messages {
			toolResultSeen = toolResultSeen || message.Role == "tool"
		}
		w.Header().Set("Content-Type", "application/json")
		message := map[string]any{"content": "bounded-agent-result"}
		if !toolResultSeen {
			toolName := ""
			for _, tool := range request.Tools {
				if strings.HasPrefix(tool.Function.Name, "mcp:") {
					toolName = tool.Function.Name
					break
				}
			}
			if toolName != "" {
				message = map[string]any{"content": "", "tool_calls": []map[string]any{{
					"id": "e2e-tool-call", "type": "function", "function": map[string]any{
						"name": toolName, "arguments": `{"id":"agent-evidence"}`},
				}}}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": message}}, "model": "qwen-plus",
			"usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16},
		})
	})
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil || len(request.Input) == 0 {
			http.Error(w, "invalid embedding request", http.StatusBadRequest)
			return
		}
		count := embedRequests.Add(1)
		_ = os.WriteFile(embedEvidencePath, []byte("requests="+decimalString(count)+"\n"), 0o600)
		data := make([]map[string]any, len(request.Input))
		for i := range request.Input {
			vector := make([]float32, 1024)
			vector[0] = 1
			data[i] = map[string]any{"index": i, "embedding": vector}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "text-embedding-v3",
			"usage": map[string]int{"prompt_tokens": len(request.Input), "total_tokens": len(request.Input)}})
	})

	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic("LLM E2E server failed")
	}
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return value
}

func decimalString(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}

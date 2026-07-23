//go:build ignore

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type rpcRequest struct {
	ID     int            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func main() {
	address := required("E2E_MCP_ADDRESS")
	evidencePath := required("E2E_MCP_EVIDENCE")
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request rpcRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := any(map[string]any{})
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "e2e_lookup", "description": "Returns bounded E2E evidence",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
					"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
			}}}
		case "resources/list":
			result = map[string]any{"resources": []any{}}
		case "tools/call":
			calls.Add(1)
			if err := os.WriteFile(evidencePath, []byte("calls="+decimal(calls.Load())+"\n"), 0o600); err != nil {
				http.Error(w, "evidence unavailable", http.StatusInternalServerError)
				return
			}
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "bounded-e2e-result"}}}
		default:
			http.Error(w, "unsupported method", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	})
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic("MCP E2E server failed")
	}
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return value
}

func decimal(value int64) string {
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

package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"
)

type pendingCall struct {
	tool        string
	startedAt   time.Time
	concurrency int
}

type pendingInitialize struct {
	id        string
	startedAt time.Time
}

const maxNumericIDBytes = 256

type Tracker struct {
	mu         sync.Mutex
	now        func() time.Time
	metadata   Metadata
	pending    map[string]pendingCall
	initialize *pendingInitialize
}

func NewTracker(now func() time.Time, metadata Metadata) (*Tracker, error) {
	if now == nil {
		return nil, fmt.Errorf("clock is required")
	}
	if strings.TrimSpace(metadata.Client) == "" || strings.TrimSpace(metadata.Service) == "" ||
		strings.TrimSpace(metadata.SessionHash) == "" {
		return nil, fmt.Errorf("client, service, and session hash are required")
	}
	return &Tracker{now: now, metadata: metadata, pending: make(map[string]pendingCall)}, nil
}

func (t *Tracker) ClientMessage(raw []byte) []Event {
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return nil
	}
	switch message.Method {
	case "tools/call":
		id, ok := normalizeID(message.ID)
		if !ok {
			return nil
		}
		var params struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(message.Params, &params) != nil || strings.TrimSpace(params.Name) == "" {
			return nil
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, exists := t.pending[id]; exists || t.initialize != nil && t.initialize.id == id {
			return nil
		}
		t.pending[id] = pendingCall{tool: params.Name, startedAt: t.now(), concurrency: len(t.pending) + 1}
	case "initialize":
		id, ok := normalizeID(message.ID)
		if !ok {
			return nil
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.initialize != nil || t.pending[id].tool != "" {
			return nil
		}
		t.initialize = &pendingInitialize{id: id, startedAt: t.now()}
	case "notifications/cancelled":
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(message.Params, &params) != nil {
			return nil
		}
		id, ok := normalizeID(params.RequestID)
		if !ok {
			return nil
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if call, exists := t.pending[id]; exists {
			delete(t.pending, id)
			return []Event{t.toolEvent(call, OutcomeCancelled, false, 0)}
		}
		if t.initialize != nil && t.initialize.id == id {
			t.initialize = nil
		}
	}
	return nil
}

func (t *Tracker) ServerMessage(raw []byte) []Event {
	var message struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return nil
	}
	id, ok := normalizeID(message.ID)
	if !ok {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.initialize != nil && t.initialize.id == id {
		startedAt := t.initialize.startedAt
		t.initialize = nil
		if hasJSONValue(message.Error) || !hasJSONValue(message.Result) {
			return nil
		}
		return []Event{t.sessionEvent(startedAt, len(raw))}
	}
	if message.Result == nil && !hasJSONValue(message.Error) {
		return nil
	}
	call, exists := t.pending[id]
	if !exists {
		return nil
	}
	delete(t.pending, id)
	if hasJSONValue(message.Error) {
		return []Event{t.toolEvent(call, errorOutcome(message.Error), false, len(raw))}
	}
	if message.Result != nil {
		return []Event{t.toolEvent(call, OutcomeSuccess, effectiveResult(message.Result), len(raw))}
	}
	return nil
}

func (t *Tracker) Flush(outcome Outcome) []Event {
	if outcome != OutcomeDisconnected && outcome != OutcomeCancelled && outcome != OutcomeError && outcome != OutcomeTimeout {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.pending))
	for id := range t.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	events := make([]Event, 0, len(ids))
	for _, id := range ids {
		events = append(events, t.toolEvent(t.pending[id], outcome, false, 0))
	}
	clear(t.pending)
	t.initialize = nil
	return events
}

func (t *Tracker) toolEvent(call pendingCall, outcome Outcome, effective bool, responseBytes int) Event {
	at := t.now()
	return Event{
		Version: EventVersion, Kind: KindToolCall, At: at, Client: t.metadata.Client, Service: t.metadata.Service,
		Tool: call.tool, SessionHash: t.metadata.SessionHash, RepositoryHash: t.metadata.RepositoryHash,
		Outcome: outcome, Effective: effective, DurationMS: durationMS(at, call.startedAt), ResponseBytes: responseBytes,
		ConcurrentCalls: call.concurrency,
	}
}

func (t *Tracker) sessionEvent(startedAt time.Time, responseBytes int) Event {
	at := t.now()
	return Event{
		Version: EventVersion, Kind: KindSessionReady, At: at, Client: t.metadata.Client, Service: t.metadata.Service,
		SessionHash: t.metadata.SessionHash, RepositoryHash: t.metadata.RepositoryHash,
		DurationMS: durationMS(at, startedAt), ResponseBytes: responseBytes,
	}
}

func durationMS(at, startedAt time.Time) int64 {
	duration := at.Sub(startedAt)
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

func normalizeID(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false
	}
	if trimmed[0] != '"' && (len(trimmed) > maxNumericIDBytes || exponentTooLarge(trimmed)) {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", false
	}
	switch id := value.(type) {
	case string:
		return "s:" + id, true
	case json.Number:
		rational, ok := new(big.Rat).SetString(string(id))
		if !ok || rational.Denom().Cmp(big.NewInt(1)) != 0 {
			return "", false
		}
		return "n:" + rational.Num().String(), true
	default:
		return "", false
	}
}

func exponentTooLarge(raw []byte) bool {
	index := bytes.IndexAny(raw, "eE")
	if index < 0 {
		return false
	}
	exponent := bytes.TrimPrefix(raw[index+1:], []byte("+"))
	exponent = bytes.TrimPrefix(exponent, []byte("-"))
	value, ok := new(big.Int).SetString(string(exponent), 10)
	return !ok || value.Cmp(big.NewInt(maxNumericIDBytes)) > 0
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func errorOutcome(raw json.RawMessage) Outcome {
	var rpcError struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&rpcError) == nil {
		code := strings.ToLower(strings.TrimSpace(fmt.Sprint(rpcError.Code)))
		message := strings.ToLower(strings.TrimSpace(rpcError.Message))
		if strings.Contains(code, "timeout") || strings.Contains(code, "deadline") || code == "408" || code == "504" ||
			strings.Contains(message, "timeout") || strings.Contains(message, "deadline") {
			return OutcomeTimeout
		}
	}
	return OutcomeError
}

func effectiveResult(raw json.RawMessage) bool {
	var result struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Content) == 0 {
		return false
	}
	for _, item := range result.Content {
		var contentType string
		_ = json.Unmarshal(item["type"], &contentType)
		if contentType == "text" {
			var text string
			_ = json.Unmarshal(item["text"], &text)
			text = strings.TrimSpace(text)
			lower := strings.ToLower(text)
			if text != "" && !strings.HasPrefix(lower, "usage:") && !strings.HasPrefix(lower, "available tools:") &&
				!strings.HasPrefix(lower, "help:") {
				return true
			}
			continue
		}
		if nonemptyJSONPayload(item["data"]) || nonemptyJSONPayload(item["text"]) || nonemptyJSONPayload(item["blob"]) ||
			nestedResourcePayload(item["resource"]) {
			return true
		}
	}
	return false
}

func nestedResourcePayload(raw json.RawMessage) bool {
	var resource struct {
		Text string `json:"text"`
		Blob string `json:"blob"`
	}
	return json.Unmarshal(raw, &resource) == nil && (resource.Text != "" || resource.Blob != "")
}

func nonemptyJSONPayload(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value != ""
}

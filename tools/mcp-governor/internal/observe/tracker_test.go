package observe

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestTrackerSuccessfulEffectiveCallDoesNotRetainSecrets(t *testing.T) {
	clock := newTestClock(time.Unix(100, 0))
	tracker := newTestTracker(t, clock.Now)
	requestSecret := "unique-request-secret"
	responseSecret := "unique-response-secret"

	if events := tracker.ClientMessage([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"secret":%q}}}`,
		requestSecret,
	))); len(events) != 0 {
		t.Fatalf("ClientMessage() events = %v, want none", events)
	}
	clock.Advance(1500 * time.Millisecond)
	events := tracker.ServerMessage([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%q}]}}`, responseSecret,
	)))
	if len(events) != 1 {
		t.Fatalf("ServerMessage() event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Kind != KindToolCall || event.Tool != "search" || event.Outcome != OutcomeSuccess || !event.Effective {
		t.Fatalf("event = %+v", event)
	}
	if event.DurationMS != 1500 || event.ConcurrentCalls != 1 || event.ResponseBytes == 0 {
		t.Fatalf("event measurements = %+v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{requestSecret, responseSecret} {
		if contains(string(encoded), secret) {
			t.Fatalf("serialized event leaked %q: %s", secret, encoded)
		}
	}
}

func TestTrackerStringAndNumericIDsDoNotCollide(t *testing.T) {
	tracker := newTestTracker(t, time.Now)
	tracker.ClientMessage([]byte(`{"id":1,"method":"tools/call","params":{"name":"numeric"}}`))
	tracker.ClientMessage([]byte(`{"id":"1","method":"tools/call","params":{"name":"string"}}`))

	first := tracker.ServerMessage([]byte(`{"id":"1","result":{"content":[{"type":"text","text":"ok"}]}}`))
	second := tracker.ServerMessage([]byte(`{"id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	if len(first) != 1 || first[0].Tool != "string" || first[0].ConcurrentCalls != 2 {
		t.Fatalf("string response = %+v", first)
	}
	if len(second) != 1 || second[0].Tool != "numeric" || second[0].ConcurrentCalls != 1 {
		t.Fatalf("numeric response = %+v", second)
	}
}

func TestTrackerErrorTimeoutAndCancellation(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     Outcome
	}{
		{name: "error", response: `{"id":1,"error":{"code":-32603,"message":"failed"}}`, want: OutcomeError},
		{name: "timeout message", response: `{"id":1,"error":{"code":-32603,"message":" DEADLINE exceeded "}}`, want: OutcomeTimeout},
		{name: "timeout code string", response: `{"id":1,"error":{"code":"TIMEOUT","message":"failed"}}`, want: OutcomeTimeout},
		{name: "timeout code number", response: `{"id":1,"error":{"code":408,"message":"failed"}}`, want: OutcomeTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := newTestTracker(t, time.Now)
			tracker.ClientMessage([]byte(`{"id":1,"method":"tools/call","params":{"name":"work"}}`))
			events := tracker.ServerMessage([]byte(tt.response))
			if len(events) != 1 || events[0].Outcome != tt.want || events[0].Effective {
				t.Fatalf("events = %+v, want outcome %q", events, tt.want)
			}
		})
	}

	tracker := newTestTracker(t, time.Now)
	tracker.ClientMessage([]byte(`{"id":"x","method":"tools/call","params":{"name":"work"}}`))
	events := tracker.ClientMessage([]byte(`{"method":"notifications/cancelled","params":{"requestId":"x","reason":"secret"}}`))
	if len(events) != 1 || events[0].Outcome != OutcomeCancelled || events[0].ResponseBytes != 0 {
		t.Fatalf("cancel events = %+v", events)
	}
	if got := tracker.ServerMessage([]byte(`{"id":"x","result":{}}`)); len(got) != 0 {
		t.Fatalf("cancelled call remained pending: %+v", got)
	}
}

func TestTrackerEffectiveDecisions(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   bool
	}{
		{name: "null", result: `null`, want: false},
		{name: "empty content", result: `{"content":[]}`, want: false},
		{name: "blank text", result: `{"content":[{"type":"text","text":"  "}]}`, want: false},
		{name: "usage help", result: `{"content":[{"type":"text","text":" Usage: tool x"}]}`, want: false},
		{name: "available tools help", result: `{"content":[{"type":"text","text":"AVAILABLE TOOLS: x"}]}`, want: false},
		{name: "help", result: `{"content":[{"type":"text","text":"help: details"}]}`, want: false},
		{name: "useful text", result: `{"content":[{"type":"text","text":"answer"}]}`, want: true},
		{name: "image data", result: `{"content":[{"type":"image","data":"abc","mimeType":"image/png"}]}`, want: true},
		{name: "resource text", result: `{"content":[{"type":"resource","resource":{"uri":"x","text":"body"}}]}`, want: true},
		{name: "resource blob", result: `{"content":[{"type":"resource","resource":{"uri":"x","blob":"abc"}}]}`, want: true},
		{name: "empty nontext", result: `{"content":[{"type":"image","data":""}]}`, want: false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := newTestTracker(t, time.Now)
			request := fmt.Sprintf(`{"id":%d,"method":"tools/call","params":{"name":"work"}}`, i)
			tracker.ClientMessage([]byte(request))
			response := fmt.Sprintf(`{"id":%d,"result":%s}`, i, tt.result)
			events := tracker.ServerMessage([]byte(response))
			if len(events) != 1 || events[0].Effective != tt.want {
				t.Fatalf("events = %+v, want effective %v", events, tt.want)
			}
		})
	}
}

func TestTrackerIgnoresMalformedUnrelatedAndInvalidMessages(t *testing.T) {
	tracker := newTestTracker(t, time.Now)
	messages := []string{
		`{`,
		`{"method":"other","id":1}`,
		`{"method":"tools/call","id":null,"params":{"name":"x"}}`,
		`{"method":"tools/call","id":{},"params":{"name":"x"}}`,
		`{"method":"tools/call","id":1.5,"params":{"name":"x"}}`,
		`{"method":"tools/call","id":1e-2,"params":{"name":"x"}}`,
		`{"method":"tools/call","id":1,"params":{}}`,
		`{"method":"tools/call","id":1,"params":{"name":" "}}`,
	}
	for _, message := range messages {
		if events := tracker.ClientMessage([]byte(message)); len(events) != 0 {
			t.Fatalf("ClientMessage(%s) = %+v", message, events)
		}
	}
	if events := tracker.ServerMessage([]byte(`{"id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`)); len(events) != 0 {
		t.Fatalf("invalid calls changed state: %+v", events)
	}
}

func TestTrackerDuplicateIDDoesNotReplacePendingCall(t *testing.T) {
	tracker := newTestTracker(t, time.Now)
	tracker.ClientMessage([]byte(`{"id":1,"method":"tools/call","params":{"name":"first"}}`))
	tracker.ClientMessage([]byte(`{"id":1,"method":"tools/call","params":{"name":"second"}}`))
	events := tracker.ServerMessage([]byte(`{"id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	if len(events) != 1 || events[0].Tool != "first" || events[0].ConcurrentCalls != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func TestTrackerUnrelatedServerMessageDoesNotRemovePendingCall(t *testing.T) {
	tracker := newTestTracker(t, time.Now)
	tracker.ClientMessage([]byte(`{"id":1,"method":"tools/call","params":{"name":"first"}}`))
	if events := tracker.ServerMessage([]byte(`{"id":1,"method":"notification"}`)); len(events) != 0 {
		t.Fatalf("unrelated response emitted events: %+v", events)
	}
	events := tracker.ServerMessage([]byte(`{"id":1,"result":{}}`))
	if len(events) != 1 || events[0].Tool != "first" {
		t.Fatalf("pending call was removed: %+v", events)
	}
}

func TestTrackerInitializeLatencyAndCancellation(t *testing.T) {
	clock := newTestClock(time.Unix(200, 0))
	tracker := newTestTracker(t, clock.Now)
	tracker.ClientMessage([]byte(`{"id":"init","method":"initialize","params":{"secret":"ignored"}}`))
	clock.Advance(2500 * time.Millisecond)
	raw := []byte(`{"id":"init","result":{"protocolVersion":"2025-06-18"}}`)
	events := tracker.ServerMessage(raw)
	if len(events) != 1 || events[0].Kind != KindSessionReady || events[0].DurationMS != 2500 || events[0].ResponseBytes != len(raw) {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Tool != "" || events[0].Outcome != "" || events[0].Effective {
		t.Fatalf("session event contains tool fields: %+v", events[0])
	}

	tracker.ClientMessage([]byte(`{"id":2,"method":"initialize"}`))
	if got := tracker.ClientMessage([]byte(`{"method":"notifications/cancelled","params":{"requestId":2}}`)); len(got) != 0 {
		t.Fatalf("initialize cancellation emitted %+v", got)
	}
	if got := tracker.ServerMessage([]byte(`{"id":2,"result":{}}`)); len(got) != 0 {
		t.Fatalf("cancelled initialize remained pending: %+v", got)
	}
}

func TestTrackerNegativeClockClampsDuration(t *testing.T) {
	clock := newTestClock(time.Unix(300, 0))
	tracker := newTestTracker(t, clock.Now)
	tracker.ClientMessage([]byte(`{"id":1,"method":"tools/call","params":{"name":"work"}}`))
	clock.Advance(-time.Second)
	events := tracker.ServerMessage([]byte(`{"id":1,"result":{}}`))
	if len(events) != 1 || events[0].DurationMS != 0 {
		t.Fatalf("events = %+v", events)
	}
}

func TestTrackerFlushIsDeterministicAndClearsState(t *testing.T) {
	clock := newTestClock(time.Unix(400, 0))
	tracker := newTestTracker(t, clock.Now)
	tracker.ClientMessage([]byte(`{"id":"2","method":"tools/call","params":{"name":"string-two"}}`))
	tracker.ClientMessage([]byte(`{"id":10,"method":"tools/call","params":{"name":"numeric-ten"}}`))
	tracker.ClientMessage([]byte(`{"id":2,"method":"tools/call","params":{"name":"numeric-two"}}`))
	tracker.ClientMessage([]byte(`{"id":"init","method":"initialize"}`))
	clock.Advance(time.Second)
	events := tracker.Flush(OutcomeDisconnected)
	if len(events) != 3 {
		t.Fatalf("Flush() = %+v", events)
	}
	wantTools := []string{"numeric-ten", "numeric-two", "string-two"}
	for i, want := range wantTools {
		if events[i].Tool != want || events[i].Outcome != OutcomeDisconnected || events[i].DurationMS != 1000 {
			t.Fatalf("event[%d] = %+v, want tool %q", i, events[i], want)
		}
	}
	if got := tracker.Flush(OutcomeCancelled); len(got) != 0 {
		t.Fatalf("second Flush() = %+v", got)
	}
	if got := tracker.Flush(OutcomeSuccess); len(got) != 0 {
		t.Fatalf("unsupported Flush() = %+v", got)
	}
}

func TestTrackerConcurrentAccess(t *testing.T) {
	tracker := newTestTracker(t, time.Now)
	const calls = 100
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			tracker.ClientMessage([]byte(fmt.Sprintf(`{"id":%d,"method":"tools/call","params":{"name":"work"}}`, id)))
		}(i)
		go func(id int) {
			defer wg.Done()
			tracker.ServerMessage([]byte(fmt.Sprintf(`{"id":%d,"result":{"content":[{"type":"text","text":"ok"}]}}`, id)))
		}(i)
	}
	wg.Wait()
	tracker.Flush(OutcomeDisconnected)
}

func TestTrackerMetadataAndEventValidation(t *testing.T) {
	metadata := Metadata{Client: "client", Service: "service", SessionHash: "session", RepositoryHash: "repo"}
	if _, err := NewTracker(nil, metadata); err == nil {
		t.Fatal("NewTracker(nil) error = nil")
	}
	for _, mutate := range []func(*Metadata){
		func(m *Metadata) { m.Client = " " },
		func(m *Metadata) { m.Service = "" },
		func(m *Metadata) { m.SessionHash = "\t" },
	} {
		invalid := metadata
		mutate(&invalid)
		if _, err := NewTracker(time.Now, invalid); err == nil {
			t.Fatalf("NewTracker(%+v) error = nil", invalid)
		}
	}

	valid := Event{Version: EventVersion, Kind: KindToolCall, At: time.Now(), Client: "c", Service: "s", Tool: "t", SessionHash: "h", Outcome: OutcomeSuccess}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid.Validate() = %v", err)
	}
	invalidEvents := []Event{
		{},
		withEvent(valid, func(e *Event) { e.Version = 2 }),
		withEvent(valid, func(e *Event) { e.Kind = "unknown" }),
		withEvent(valid, func(e *Event) { e.At = time.Time{} }),
		withEvent(valid, func(e *Event) { e.Client = " " }),
		withEvent(valid, func(e *Event) { e.Service = "" }),
		withEvent(valid, func(e *Event) { e.SessionHash = "" }),
		withEvent(valid, func(e *Event) { e.Tool = "" }),
		withEvent(valid, func(e *Event) { e.Outcome = "unknown" }),
		withEvent(valid, func(e *Event) { e.DurationMS = -1 }),
		withEvent(valid, func(e *Event) { e.ResponseBytes = -1 }),
		withEvent(valid, func(e *Event) { e.ConcurrentCalls = -1 }),
	}
	for i, event := range invalidEvents {
		if err := event.Validate(); err == nil {
			t.Fatalf("invalid event %d validated: %+v", i, event)
		}
	}
	session := Event{Version: EventVersion, Kind: KindSessionReady, At: time.Now(), Client: "c", Service: "s", SessionHash: "h"}
	if err := session.Validate(); err != nil {
		t.Fatalf("session.Validate() = %v", err)
	}
	for _, event := range []Event{
		withEvent(session, func(e *Event) { e.Tool = "x" }),
		withEvent(session, func(e *Event) { e.Outcome = OutcomeSuccess }),
		withEvent(session, func(e *Event) { e.Effective = true }),
		withEvent(session, func(e *Event) { e.DurationMS = -1 }),
	} {
		if err := event.Validate(); err == nil {
			t.Fatalf("invalid session validated: %+v", event)
		}
	}
}

func newTestTracker(t *testing.T, now func() time.Time) *Tracker {
	t.Helper()
	tracker, err := NewTracker(now, Metadata{Client: "client", Service: "service", SessionHash: "session", RepositoryHash: "repo"})
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock { return &testClock{now: now} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

func withEvent(event Event, mutate func(*Event)) Event {
	mutate(&event)
	return event
}

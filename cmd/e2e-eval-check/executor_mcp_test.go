package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/byteBuilderX/stratum/internal/mcp/infrastructure/testserver"
)

// fakeInvoker returns canned tool output, recording calls for assertions.
type fakeInvoker struct {
	outputs map[string]string
	calls   []callRecord
}

type callRecord struct {
	server, tool string
	args         map[string]any
}

func (f *fakeInvoker) CallTool(ctx context.Context, server, tool string, args map[string]any) (string, error) {
	f.calls = append(f.calls, callRecord{server, tool, args})
	return f.outputs[server+"."+tool], nil
}

func TestMCPExecutorExactMatch(t *testing.T) {
	inv := &fakeInvoker{outputs: map[string]string{"srv.get_weather": `{"city":"Beijing","temp":22}`}}
	ex := &mcpExecutor{invoker: inv}
	p := point{Kind: "mcp", Snapshot: map[string]any{"servers": []any{"srv"}}}
	dataset := goldenSet{Version: 1, Cases: []goldenCase{{
		ID: "w1", Query: `srv/get_weather {"city":"Beijing"}`, Mode: "exact",
		ExpectedOutput: `{"city":"Beijing","temp":22}`,
	}}}
	// mcp cases map Query→"server/tool {json-args}" tool spec, Mode→assertion
	// mode, ExpectedOutput→expected value. parseToolCall splits Query.
	res, err := ex.runCases(context.Background(), p, dataset)
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if len(res.Cases) != 1 || !res.Cases[0].Passed {
		t.Fatalf("expected exact match to pass: %+v", res.Cases)
	}
	if res.Aggregate.PassRate != 1 {
		t.Fatalf("pass_rate = %f, want 1", res.Aggregate.PassRate)
	}
	if len(inv.calls) != 1 || inv.calls[0].server != "srv" || inv.calls[0].tool != "get_weather" {
		t.Fatalf("expected one call to srv.get_weather, got %+v", inv.calls)
	}
	if got := inv.calls[0].args["city"]; got != "Beijing" {
		t.Fatalf("tool arg city = %v, want Beijing", got)
	}
}

func TestMCPExecutorContainsMismatch(t *testing.T) {
	inv := &fakeInvoker{outputs: map[string]string{"srv.get_weather": `{"city":"Shanghai"}`}}
	ex := &mcpExecutor{invoker: inv}
	p := point{Kind: "mcp", Snapshot: map[string]any{"servers": []any{"srv"}}}
	dataset := goldenSet{Version: 1, Cases: []goldenCase{{
		ID: "w2", Query: `srv/get_weather {"city":"Beijing"}`, Mode: "contains",
		ExpectedOutput: "Beijing",
	}}}
	res, err := ex.runCases(context.Background(), p, dataset)
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if res.Cases[0].Passed {
		t.Fatalf("expected contains mismatch to fail: %+v", res.Cases[0])
	}
	if res.Aggregate.PassRate != 0 {
		t.Fatalf("pass_rate = %f, want 0", res.Aggregate.PassRate)
	}
}

func TestMCPExecutorRegexMode(t *testing.T) {
	inv := &fakeInvoker{outputs: map[string]string{"srv.get_weather": `{"city":"Beijing","temp":22}`}}
	ex := &mcpExecutor{invoker: inv}
	dataset := goldenSet{Version: 1, Cases: []goldenCase{{
		ID: "w3", Query: `srv/get_weather`, Mode: "regex",
		ExpectedOutput: `"temp":\d+`,
	}}}
	res, err := ex.runCases(context.Background(), point{Kind: "mcp"}, dataset)
	if err != nil {
		t.Fatalf("runCases: %v", err)
	}
	if !res.Cases[0].Passed {
		t.Fatalf("expected regex match to pass: %+v", res.Cases[0])
	}
}

func TestMCPExecutorBadToolSpecFails(t *testing.T) {
	inv := &fakeInvoker{outputs: map[string]string{}}
	ex := &mcpExecutor{invoker: inv}
	dataset := goldenSet{Version: 1, Cases: []goldenCase{{
		ID: "w4", Query: `get_weather`, Mode: "exact", ExpectedOutput: "x",
	}}}
	if _, err := ex.runCases(context.Background(), point{Kind: "mcp"}, dataset); err == nil {
		t.Fatal("expected tool spec parse error")
	}
}

func TestLiveMCPInvokerAgainstFixture(t *testing.T) {
	srv := testserver.NewSDKServer(t, []testserver.Tool{{
		Name:        "echo",
		Description: "echo a string",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
		},
	}})
	inv := &liveMCPInvoker{
		servers:  map[string]mcpServerConfig{"fx": {URL: srv.URL()}},
		sessions: map[string]*mcp.ClientSession{},
	}
	out, err := inv.CallTool(context.Background(), "fx", "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "ok" {
		t.Fatalf("expected tool output %q, got %q", "ok", out)
	}
	// Close the session so the SDK client's SSE stream is torn down before the
	// fixture server's cleanup runs; otherwise httptest.Server.Close blocks.
	if err := inv.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestMCPFingerprintDeterministic(t *testing.T) {
	snapshot := map[string]any{"servers": []any{
		map[string]any{"name": "a", "url": "http://x"},
		map[string]any{"name": "b", "url": "http://y"},
	}}
	first := mcpFingerprint(snapshot)
	for i := 0; i < 5; i++ {
		if got := mcpFingerprint(snapshot); got.Hash != first.Hash {
			t.Fatalf("fingerprint not deterministic: %s != %s", got.Hash, first.Hash)
		}
	}
}

package main

import (
	"testing"
)

func TestParseOptions(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    options
		wantErr string
	}{
		{name: "minimal", args: []string{"--kind", "knowledge", "--point", "p1"}, want: options{kind: "knowledge", point: "p1", warnDelta: 0.1}},
		{name: "default warn delta", args: []string{"--kind", "mcp", "--point", "p2"}, want: options{kind: "mcp", point: "p2", warnDelta: 0.1}},
		{name: "custom warn delta", args: []string{"--kind", "agent", "--point", "p3", "--warn-delta", "0.2"}, want: options{kind: "agent", point: "p3", warnDelta: 0.2}},
		{name: "fail-on-warn", args: []string{"--kind", "mcp", "--point", "p", "--fail-on-warn"}, want: options{kind: "mcp", point: "p", warnDelta: 0.1, failOnWarn: true}},
		{name: "record with confirm", args: []string{"--kind", "mcp", "--point", "p", "--record-baseline", "--confirm-record"}, want: options{kind: "mcp", point: "p", warnDelta: 0.1, recordBaseline: true, confirmRecord: true}},
		{name: "skip with reason", args: []string{"--kind", "agent", "--point", "p", "--skip", "no real LLM in CI"}, want: options{kind: "agent", point: "p", warnDelta: 0.1, skip: "no real LLM in CI"}},
		{name: "missing kind", args: []string{"--point", "p"}, wantErr: "--kind is required"},
		{name: "missing point", args: []string{"--kind", "mcp"}, wantErr: "--point is required"},
		{name: "invalid kind", args: []string{"--kind", "llm", "--point", "p"}, wantErr: "unsupported kind"},
		{name: "skip without reason", args: []string{"--kind", "mcp", "--point", "p", "--skip"}, wantErr: "--skip requires a reason"},
		{name: "record without confirm", args: []string{"--kind", "mcp", "--point", "p", "--record-baseline"}, wantErr: "--record-baseline requires --confirm-record"},
		{name: "invalid warn delta", args: []string{"--kind", "mcp", "--point", "p", "--warn-delta", "-1"}, wantErr: "--warn-delta must be in [0,1]"},
		{name: "self check without kind", args: []string{"--self-check"}, want: options{warnDelta: 0.1, selfCheck: true}},
		{name: "self check with kind", args: []string{"--self-check", "--kind", "knowledge"}, want: options{kind: "knowledge", warnDelta: 0.1, selfCheck: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOptions(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseOptions(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

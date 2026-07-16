package process

import (
	"strings"
	"testing"
)

func TestDecodeRegistryValid(t *testing.T) {
	registry, err := DecodeRegistry(strings.NewReader(`{
		"version":1,
		"registrations":[{
			"identity":{"pid":10,"start_ticks":100},
			"client":{"pid":20,"start_ticks":200},
			"service":"obsidian",
			"repository":"/repo",
			"connected_at":"2026-07-16T01:02:03Z"
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodeRegistry: %v", err)
	}
	if registry.Version != 1 || len(registry.Registrations) != 1 {
		t.Fatalf("registry = %#v", registry)
	}
	registration := registry.Registrations[0]
	if registration.Identity != (Identity{PID: 10, StartTicks: 100}) ||
		registration.Client != (Identity{PID: 20, StartTicks: 200}) ||
		registration.Service != "obsidian" || registration.Repository != "/repo" {
		t.Errorf("registration = %#v", registration)
	}
}

func TestDecodeRegistryRejectsInvalidInput(t *testing.T) {
	valid := `{"version":1,"registrations":[{"identity":{"pid":10,"start_ticks":100},"client":{"pid":20,"start_ticks":200},"service":"obsidian","connected_at":"2026-07-16T01:02:03Z"}]}`
	tests := map[string]string{
		"malformed":            `{`,
		"unknown top field":    `{"version":1,"registrations":[],"extra":true}`,
		"unknown nested":       `{"version":1,"registrations":[{"identity":{"pid":10,"start_ticks":100,"extra":true},"client":{"pid":20,"start_ticks":200},"service":"obsidian","connected_at":"2026-07-16T01:02:03Z"}]}`,
		"trailing JSON":        valid + `{}`,
		"unsupported version":  `{"version":2,"registrations":[]}`,
		"invalid child PID":    strings.Replace(valid, `"pid":10`, `"pid":0`, 1),
		"invalid child start":  strings.Replace(valid, `"start_ticks":100`, `"start_ticks":0`, 1),
		"invalid client PID":   strings.Replace(valid, `"pid":20`, `"pid":-1`, 1),
		"invalid client start": strings.Replace(valid, `"start_ticks":200`, `"start_ticks":0`, 1),
		"empty service":        strings.Replace(valid, `"service":"obsidian"`, `"service":""`, 1),
		"blank service":        strings.Replace(valid, `"service":"obsidian"`, `"service":"  "`, 1),
		"zero connected at":    strings.Replace(valid, `"2026-07-16T01:02:03Z"`, `"0001-01-01T00:00:00Z"`, 1),
		"duplicate identity":   strings.Replace(valid, `}]}`, `},{"identity":{"pid":10,"start_ticks":100},"client":{"pid":30,"start_ticks":300},"service":"other","connected_at":"2026-07-16T01:02:04Z"}]}`, 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRegistry(strings.NewReader(input)); err == nil {
				t.Fatal("DecodeRegistry succeeded; want error")
			}
		})
	}
}

func TestDecodeRegistryRejectsDuplicateJSONKeys(t *testing.T) {
	valid := `{"version":1,"registrations":[{"identity":{"pid":10,"start_ticks":100},"client":{"pid":20,"start_ticks":200},"service":"obsidian","connected_at":"2026-07-16T01:02:03Z"}]}`
	tests := []struct {
		name  string
		input string
		key   string
	}{
		{"top-level version", strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), "version"},
		{"top-level registrations", strings.Replace(valid, `"registrations":`, `"registrations":[],"registrations":`, 1), "registrations"},
		{"registration client", strings.Replace(valid, `"client":{"pid":20,"start_ticks":200}`, `"client":{"pid":20,"start_ticks":200},"client":{"pid":30,"start_ticks":300}`, 1), "client"},
		{"registration service", strings.Replace(valid, `"service":"obsidian"`, `"service":"obsidian","service":"other"`, 1), "service"},
		{"identity PID", strings.Replace(valid, `"identity":{"pid":10`, `"identity":{"pid":10,"pid":11`, 1), "pid"},
		{"identity start ticks", strings.Replace(valid, `"identity":{"pid":10,"start_ticks":100`, `"identity":{"pid":10,"start_ticks":100,"start_ticks":101`, 1), "start_ticks"},
		{"client PID", strings.Replace(valid, `"client":{"pid":20`, `"client":{"pid":20,"pid":21`, 1), "pid"},
		{"client start ticks", strings.Replace(valid, `"client":{"pid":20,"start_ticks":200`, `"client":{"pid":20,"start_ticks":200,"start_ticks":201`, 1), "start_ticks"},
		{"case-aliased top-level version", strings.Replace(valid, `"version":1`, `"version":1,"Version":1`, 1), "Version"},
		{"case-aliased registration client", strings.Replace(valid, `"client":{"pid":20,"start_ticks":200}`, `"client":{"pid":20,"start_ticks":200},"Client":{"pid":30,"start_ticks":300}`, 1), "Client"},
		{"case-aliased identity PID", strings.Replace(valid, `"identity":{"pid":10`, `"identity":{"pid":10,"PID":11`, 1), "PID"},
		{"case-aliased client start ticks", strings.Replace(valid, `"client":{"pid":20,"start_ticks":200`, `"client":{"pid":20,"start_ticks":200,"START_TICKS":201`, 1), "START_TICKS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeRegistry(strings.NewReader(tt.input))
			if err == nil {
				t.Fatal("DecodeRegistry succeeded; want error")
			}
			if !strings.Contains(err.Error(), "duplicate key") || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("error %q does not identify duplicate key %q", err, tt.key)
			}
		})
	}
}

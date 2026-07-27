package e2eattestation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestRejectsInvalidCoverage(t *testing.T) {
	t.Parallel()

	valid := validManifest()
	tests := []struct {
		name      string
		mutate    func(*Manifest)
		routes    []string
		mutations []string
		actions   map[string]struct{}
		want      string
	}{
		{
			name: "duplicate capability ID",
			mutate: func(m *Manifest) {
				m.Capabilities = append(m.Capabilities, m.Capabilities[0])
			},
			want: "duplicate capability ID",
		},
		{
			name: "unknown domain",
			mutate: func(m *Manifest) {
				m.Capabilities[0].Domain = "billing"
			},
			want: "unknown domain",
		},
		{
			name: "empty browser action ID",
			mutate: func(m *Manifest) {
				m.Capabilities[0].BrowserActionID = ""
			},
			want: "browser action ID",
		},
		{
			name: "unknown browser action ID",
			mutate: func(m *Manifest) {
				m.Capabilities[0].BrowserActionID = "agent.unknown"
			},
			want: "unknown browser action ID",
		},
		{
			name: "missing HTTP evidence",
			mutate: func(m *Manifest) {
				m.Capabilities[0].HTTPEvidence = ""
			},
			want: "HTTP evidence",
		},
		{
			name: "missing DB evidence",
			mutate: func(m *Manifest) {
				m.Capabilities[0].DBEvidence = ""
			},
			want: "DB evidence",
		},
		{
			name: "missing allowed role coverage",
			mutate: func(m *Manifest) {
				m.Capabilities[0].Roles.Allowed = nil
			},
			want: "allowed role",
		},
		{
			name: "missing denied role coverage",
			mutate: func(m *Manifest) {
				m.Capabilities[0].Roles.Denied = nil
			},
			want: "denied role",
		},
		{
			name: "missing lower-layer justification",
			mutate: func(m *Manifest) {
				m.Capabilities[0].Coverage = "lower_layer"
			},
			want: "lower-layer justification",
		},
		{
			name: "unexpected lower-layer justification",
			mutate: func(m *Manifest) {
				m.Capabilities[0].LowerLayerJustification = "unit test covers this"
			},
			want: "only valid for lower_layer",
		},
		{
			name:   "unmapped route",
			routes: []string{"/agents", "/agents/unmapped"},
			want:   "unmapped route",
		},
		{
			name:      "unmapped mutation",
			mutations: []string{"POST /agents", "DELETE /agents/:id"},
			want:      "unmapped mutation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := valid
			manifest.Capabilities = append([]Capability(nil), valid.Capabilities...)
			if tt.mutate != nil {
				tt.mutate(&manifest)
			}
			routes := tt.routes
			if routes == nil {
				routes = []string{"/agents"}
			}
			mutations := tt.mutations
			if mutations == nil {
				mutations = []string{"POST /agents"}
			}
			actions := tt.actions
			if actions == nil {
				actions = map[string]struct{}{"agent.create": {}}
			}

			err := ValidateManifest(manifest, routes, mutations, actions)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateManifest() error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadManifestRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "manifest.json")
	data := `{"version":1,"capabilities":[],"unexpected":true}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadManifest(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadManifest() error = %v, want unknown field error", err)
	}
}

func TestManifestAcceptsCompleteCoverage(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(validManifest(), []string{"/agents"}, []string{"POST /agents"}, map[string]struct{}{
		"agent.create": {},
	})
	if err != nil {
		t.Fatalf("ValidateManifest() error = %v", err)
	}
}

func TestRepositoryManifestCoversManagedSurface(t *testing.T) {
	t.Parallel()

	manifest, err := LoadManifest(filepath.Join("..", "..", "..", "test", "e2e", "stateful", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	actions := make(map[string]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		actions[capability.BrowserActionID] = struct{}{}
	}

	var surface struct {
		Routes    []string `json:"routes"`
		Mutations []string `json:"mutations"`
	}
	surfaceData, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "services", "e2e-surface.json"))
	if err != nil {
		t.Fatalf("read E2E surface: %v", err)
	}
	if err := json.Unmarshal(surfaceData, &surface); err != nil {
		t.Fatalf("decode E2E surface: %v", err)
	}
	if err := ValidateManifest(manifest, surface.Routes, surface.Mutations, actions); err != nil {
		t.Fatalf("ValidateManifest() error = %v", err)
	}
}

func validManifest() Manifest {
	return Manifest{
		Version: 1,
		Capabilities: []Capability{
			{
				ID:              "agent.create",
				Domain:          "agent",
				UserGoal:        "Create an agent",
				Route:           "/agents",
				Roles:           RoleCoverage{Allowed: []string{"tenant_admin"}, Denied: []string{"member"}},
				Mutation:        "POST /agents",
				BrowserActionID: "agent.create",
				HTTPEvidence:    "POST returns 201",
				DBEvidence:      "agent row exists in tenant schema",
				Coverage:        "short",
			},
		},
	}
}

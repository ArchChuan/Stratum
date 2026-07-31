package schema_test

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestVerificationPlanSchema(t *testing.T) {
	t.Parallel()
	schema := compileSchema(t, "verification-plan.schema.json")
	valid := basePlan()
	tests := []struct {
		name    string
		value   map[string]any
		wantErr bool
	}{
		{name: "accepts separate local and CI checks", value: valid},
		{name: "rejects missing manifest digest", value: removed(valid, "manifest_digest"), wantErr: true},
		{name: "rejects legacy shared checks", value: changed(valid, "checks", []any{"unit"}), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := schema.Validate(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func compileSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", ".test", "schemas", name)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	schema, err := compiler.Compile(path)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return schema
}

func basePlan() map[string]any {
	return map[string]any{
		"version": 1, "commit": hex(40), "manifest_digest": "sha256:" + hex(64),
		"risk_level": "R3", "mode": "soak",
		"local_checks": []any{"unit", "e2e-soak"}, "ci_checks": []any{"unit", "security"},
	}
}

func changed(src map[string]any, key string, value any) map[string]any {
	dst := clone(src)
	dst[key] = value
	return dst
}

func removed(src map[string]any, key string) map[string]any {
	dst := clone(src)
	delete(dst, key)
	return dst
}

func clone(src map[string]any) map[string]any {
	raw, _ := json.Marshal(src)
	var dst map[string]any
	_ = json.Unmarshal(raw, &dst)
	return dst
}

func hex(size int) string {
	value := make([]byte, size)
	for i := range value {
		value[i] = 'a'
	}
	return string(value)
}

package schema_test

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestVerificationSchemas(t *testing.T) {
	plan := compileSchema(t, "verification-plan.schema.json")
	report := compileSchema(t, "completion-report.schema.json")
	validPlan := basePlan()
	validReport := baseReport()

	tests := []struct {
		name    string
		schema  *jsonschema.Schema
		value   map[string]any
		wantErr bool
	}{
		{name: "accepts R3 plan", schema: plan, value: validPlan},
		{name: "accepts authoritative report", schema: report, value: validReport},
		{
			name: "accepts R0 report without reviews", schema: report,
			value: reportWithRisk(validReport, "R0", map[string]any{}),
		},
		{
			name: "rejects R1 report without code quality review", schema: report,
			value: reportWithRisk(validReport, "R1", map[string]any{}), wantErr: true,
		},
		{
			name: "accepts R1 report with code quality review", schema: report,
			value: reportWithRisk(validReport, "R1", map[string]any{"code-quality": "passed"}),
		},
		{
			name: "rejects R2 report without reviews", schema: report,
			value: reportWithRisk(validReport, "R2", map[string]any{}), wantErr: true,
		},
		{
			name: "accepts R2 report with required reviews", schema: report,
			value: reportWithRisk(validReport, "R2", map[string]any{
				"specification": "passed", "code-quality": "passed",
			}),
		},
		{name: "accepts blocked report", schema: report, value: changed(validReport, "status", "blocked")},
		{
			name: "rejects plan missing manifest digest", schema: plan,
			value: removed(validPlan, "manifest_digest"), wantErr: true,
		},
		{name: "rejects missing commit", schema: report, value: removed(validReport, "commit"), wantErr: true},
		{
			name: "rejects report missing manifest digest", schema: report,
			value: removed(validReport, "manifest_digest"), wantErr: true,
		},
		{
			name: "rejects R3 report missing specification review", schema: report,
			value: reportWithReviews(validReport, map[string]any{"code-quality": "passed"}), wantErr: true,
		},
		{
			name: "rejects R3 report with failed specification review", schema: report,
			value: reportWithReviews(validReport, map[string]any{
				"specification": "changes_requested", "code-quality": "passed",
			}), wantErr: true,
		},
		{
			name: "rejects R3 report missing code quality review", schema: report,
			value: reportWithReviews(validReport, map[string]any{"specification": "passed"}), wantErr: true,
		},
		{
			name: "rejects R4 report missing release evidence review", schema: report,
			value: reportWithRisk(validReport, "R4", map[string]any{
				"specification": "passed", "code-quality": "passed",
			}), wantErr: true,
		},
		{
			name: "accepts R4 report with required reviews", schema: report,
			value: reportWithRisk(validReport, "R4", map[string]any{
				"specification": "passed", "code-quality": "passed", "release-evidence": "passed",
			}),
		},
		{
			name: "rejects skipped accepted capability", schema: report,
			value: changedCount(validReport, "skipped", 1), wantErr: true,
		},
		{
			name: "rejects unverified accepted attestation", schema: report,
			value: changedNested(validReport, "attestation", "verified", false), wantErr: true,
		},
		{
			name: "rejects incomplete accepted cleanup", schema: report,
			value: changedNested(validReport, "cleanup", "complete", false), wantErr: true,
		},
		{
			name: "rejects accepted report without artifact", schema: report,
			value: changed(validReport, "artifacts", []any{}), wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.schema.Validate(tt.value)
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
		"risk_level": "R3", "mode": "soak", "checks": []any{"unit", "e2e-soak"},
		"reviews": []any{"specification", "code-quality"},
	}
}

func baseReport() map[string]any {
	return map[string]any{
		"version": 1, "status": "accepted", "commit": hex(40),
		"manifest_digest": "sha256:" + hex(64), "risk_level": "R3", "mode": "soak",
		"reviews": map[string]any{"specification": "passed", "code-quality": "passed"},
		"capabilities": map[string]any{
			"passed": 3, "failed": 0, "blocked": 0, "skipped": 0, "unreconciled": 0,
		},
		"attestation": map[string]any{
			"schema": 2, "verified": true, "path": "test/e2e/attestations/result.json",
		},
		"cleanup":   map[string]any{"complete": true, "residual_entities": 0},
		"artifacts": []any{"sha256:" + hex(64)},
	}
}

func changed(src map[string]any, key string, value any) map[string]any {
	dst := clone(src)
	dst[key] = value
	return dst
}

func reportWithReviews(src map[string]any, reviews map[string]any) map[string]any {
	return changed(src, "reviews", reviews)
}

func reportWithRisk(src map[string]any, risk string, reviews map[string]any) map[string]any {
	return changed(reportWithReviews(src, reviews), "risk_level", risk)
}

func removed(src map[string]any, key string) map[string]any {
	dst := clone(src)
	delete(dst, key)
	return dst
}

func changedCount(src map[string]any, key string, value int) map[string]any {
	return changedNested(src, "capabilities", key, value)
}

func changedNested(src map[string]any, parent, key string, value any) map[string]any {
	dst := clone(src)
	dst[parent].(map[string]any)[key] = value
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

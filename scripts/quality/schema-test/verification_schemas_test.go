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
			value: reportWithoutE2E(validReport, "R0", []any{}),
		},
		{
			name: "rejects R1 report without code quality review", schema: report,
			value: reportWithRisk(validReport, "R1", []any{}), wantErr: true,
		},
		{
			name: "accepts R1 report with code quality review", schema: report,
			value: reportWithoutE2E(validReport, "R1", []any{review("code-quality", "passed")}),
		},
		{
			name: "rejects accepted report without signed E2E evidence", schema: report,
			value: changed(reportWithoutE2E(validReport, "R0", []any{}), "status", "accepted"), wantErr: true,
		},
		{
			name: "rejects none mode with an E2E attestation", schema: report,
			value: changed(reportWithRisk(validReport, "R0", []any{}), "mode", "none"), wantErr: true,
		},
		{
			name: "rejects R2 report without reviews", schema: report,
			value: reportWithRisk(validReport, "R2", []any{}), wantErr: true,
		},
		{
			name: "accepts R2 report with required reviews", schema: report,
			value: reportWithRisk(validReport, "R2", []any{
				review("specification", "passed"), review("code-quality", "passed"),
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
			value: reportWithReviews(validReport, []any{review("code-quality", "passed")}), wantErr: true,
		},
		{
			name: "rejects R3 report with failed specification review", schema: report,
			value: reportWithReviews(validReport, []any{
				review("specification", "changes_requested"), review("code-quality", "passed"),
			}), wantErr: true,
		},
		{
			name: "rejects R3 report missing code quality review", schema: report,
			value: reportWithReviews(validReport, []any{review("specification", "passed")}), wantErr: true,
		},
		{
			name: "rejects R4 report missing release evidence review", schema: report,
			value: reportWithRisk(validReport, "R4", []any{
				review("specification", "passed"), review("code-quality", "passed"),
			}), wantErr: true,
		},
		{
			name: "accepts R4 report with required reviews", schema: report,
			value: reportWithRisk(validReport, "R4", []any{
				review("specification", "passed"), review("code-quality", "passed"),
				review("release-evidence", "passed"),
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
			name: "rejects unsigned accepted attestation", schema: report,
			value: changedNested(validReport, "attestation", "signed", false), wantErr: true,
		},
		{
			name: "rejects accepted report with a failed planned check", schema: report,
			value: changedCheckStatus(validReport, "failed"), wantErr: true,
		},
		{
			name: "rejects R4 report without release artifact", schema: report,
			value: changed(reportWithRisk(validReport, "R4", []any{
				review("specification", "passed"), review("code-quality", "passed"),
				review("release-evidence", "passed"),
			}), "artifacts", []any{}), wantErr: true,
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
		"risk_level": "R3", "mode": "soak",
		"local_checks": []any{"unit", "e2e-soak"}, "ci_checks": []any{"unit", "security"},
	}
}

func baseReport() map[string]any {
	return map[string]any{
		"version": 1, "status": "accepted", "commit": hex(40),
		"manifest_digest": "sha256:" + hex(64), "risk_level": "R3", "mode": "soak",
		"reviews": []any{review("specification", "passed"), review("code-quality", "passed")},
		"checks":  []any{check("static", "passed"), check("unit", "passed")},
		"capabilities": map[string]any{
			"passed": 3, "failed": 0, "blocked": 0, "skipped": 0, "unreconciled": 0,
		},
		"attestation": map[string]any{
			"schema": 2, "verified": true, "signed": true, "issuer": "github-actions-sigstore",
			"path": "test/e2e/attestations/result.json", "bundle": "tmp/attestation.sigstore.json",
		},
		"cleanup":   map[string]any{"complete": true, "residual_entities": 0},
		"artifacts": []any{"sha256:" + hex(64)},
	}
}

func check(id, status string) map[string]any {
	return map[string]any{"id": id, "status": status, "evidence": "github-run:1"}
}

func changedCheckStatus(src map[string]any, status string) map[string]any {
	dst := clone(src)
	dst["checks"].([]any)[0].(map[string]any)["status"] = status
	return dst
}

func changed(src map[string]any, key string, value any) map[string]any {
	dst := clone(src)
	dst[key] = value
	return dst
}

func reportWithReviews(src map[string]any, reviews []any) map[string]any {
	return changed(src, "reviews", reviews)
}

func reportWithRisk(src map[string]any, risk string, reviews []any) map[string]any {
	return changed(reportWithReviews(src, reviews), "risk_level", risk)
}

func reportWithoutE2E(src map[string]any, risk string, reviews []any) map[string]any {
	report := reportWithRisk(src, risk, reviews)
	report["status"] = "incomplete"
	report["mode"] = "none"
	report["attestation"] = nil
	report["capabilities"] = map[string]any{
		"passed": 0, "failed": 0, "blocked": 0, "skipped": 0, "unreconciled": 0,
	}
	return report
}

func review(reviewType, status string) map[string]any {
	return map[string]any{
		"type": reviewType, "status": status, "reviewer": "github-environment:" + reviewType,
		"commit": hex(40), "policy_version": 1, "findings": []any{}, "evidence": "github-run:1",
	}
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

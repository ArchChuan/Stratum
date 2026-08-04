package schema_test

import (
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestAuthoritySchemas(t *testing.T) {
	t.Parallel()
	local := compileSchema(t, "local-verification.schema.json")
	ci := compileSchema(t, "ci-verification.schema.json")
	release := compileSchema(t, "release-verification.schema.json")
	tests := []struct {
		name    string
		schema  *jsonschema.Schema
		value   map[string]any
		wantErr bool
	}{
		{name: "accepts passed local verification", schema: local, value: localReport()},
		{name: "rejects passed local verification with skipped capability", schema: local,
			value: changedLocalCount(localReport(), "skipped", 1), wantErr: true},
		{name: "rejects passed local verification with incomplete cleanup", schema: local,
			value: changedLocalCleanup(localReport(), false), wantErr: true},
		{name: "accepts CI verification", schema: ci, value: ciReport()},
		{name: "rejects unknown CI status", schema: ci,
			value: changed(ciReport(), "status", "accepted"), wantErr: true},
		{name: "accepts deployed release verification", schema: release, value: releaseReport()},
		{name: "rejects mutable release image", schema: release,
			value: changedReleaseImage(releaseReport(), "registry/stratum:latest"), wantErr: true},
		{name: "rejects release receipt missing prior_digests", schema: release,
			value: withoutKey(releaseReport(), "prior_digests"), wantErr: true},
		{name: "rejects release receipt with unknown rollback basis", schema: release,
			value: changed(releaseReport(), "rollback_basis", "unknown"), wantErr: true},
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

func localReport() map[string]any {
	return map[string]any{
		"version": 1, "status": "passed", "tested_commit": hex(40),
		"manifest_digest": "sha256:" + hex(64), "risk_level": "R3", "mode": "soak",
		"source_clean": true, "attestation_path": "test/e2e/attestations/run/result.json",
		"capabilities": map[string]any{
			"passed": 3, "failed": 0, "blocked": 0, "skipped": 0, "unreconciled": 0,
		},
		"cleanup": map[string]any{"complete": true, "residual_entities": 0},
	}
}

func ciReport() map[string]any {
	return map[string]any{
		"version": 1, "status": "passed", "commit": hex(40),
		"checks": map[string]any{
			"static": "passed", "unit": "passed", "integration": "passed",
			"contract": "passed", "build": "passed", "security": "passed",
		},
	}
}

func releaseReport() map[string]any {
	digest := "registry/stratum@sha256:" + hex(64)
	return map[string]any{
		"version": 2, "status": "deployed", "commit": hex(40),
		"images": map[string]any{
			"backend": digest, "frontend": digest, "feishu_adapter": digest,
		},
		"prior_digests": map[string]any{
			"backend": "none", "frontend": "none", "feishu_adapter": "none",
		},
		"rollback_basis":  "first_deploy",
		"migration_check": "passed", "health_check": "passed", "rollback_check": "pending",
	}
}

func changedLocalCount(src map[string]any, key string, value int) map[string]any {
	dst := clone(src)
	dst["capabilities"].(map[string]any)[key] = value
	return dst
}

func changedLocalCleanup(src map[string]any, complete bool) map[string]any {
	dst := clone(src)
	dst["cleanup"].(map[string]any)["complete"] = complete
	return dst
}

func changedReleaseImage(src map[string]any, image string) map[string]any {
	dst := clone(src)
	dst["images"].(map[string]any)["backend"] = image
	return dst
}

func withoutKey(src map[string]any, key string) map[string]any {
	dst := clone(src)
	delete(dst, key)
	return dst
}

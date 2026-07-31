package e2eattestation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVerifyAttestationRejectsInvalidClaims(t *testing.T) {
	t.Parallel()
	root, report, now := validAttestationFixture(t)
	tests := []struct {
		name   string
		mutate func(*Attestation)
		want   string
	}{
		{"manifest mismatch", func(a *Attestation) { a.ManifestDigest = strings.Repeat("0", 64) }, "manifest digest"},
		{"verification policy mismatch", func(a *Attestation) {
			a.PolicyManifestDigest = strings.Repeat("0", 64)
		}, "verification policy digest"},
		{"missing pack", func(a *Attestation) { a.Packs = a.Packs[1:] }, "required pack"},
		{"skipped capability", func(a *Attestation) { a.Capabilities[0].Status = StatusSkipped }, "capability"},
		{"missing capability", func(a *Attestation) { a.Capabilities = nil }, "required capability"},
		{"failed cleanup", func(a *Attestation) { a.Cleanup.Passed = false }, "cleanup"},
		{"artifact hash mismatch", func(a *Attestation) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "test/e2e/attestations/safe.log"), []byte("mutated"), 0o600))
		}, "artifact hash"},
		{"expired", func(a *Attestation) { a.ExpiresAt = now.Add(-time.Second) }, "expired"},
		{"credential pattern", func(a *Attestation) {
			a.RiskClassification = "Authorization: Bearer fixture-secret"
		}, "credential"},
		{"soak required", func(a *Attestation) { a.Mode = "short" }, "required mode"},
		{"source mutation", func(*Attestation) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed"), 0o600))
		}, "source digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copy := cloneAttestation(t, report)
			tt.mutate(&copy)
			requiredMode := ""
			if tt.name == "soak required" {
				requiredMode = "soak"
			}
			err := VerifyAttestation(root, copy, VerifyOptions{
				Now: now, ManifestPath: "manifest.json", PolicyManifestPath: "verification.yaml",
				RequiredMode:  requiredMode,
				RequiredPacks: []string{"agent", "iam"},
			})
			require.ErrorContains(t, err, tt.want)
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "test/e2e/attestations/safe.log"), []byte("safe evidence"), 0o600,
			))
		})
	}
}

func TestVerifyAttestationEnforcesAcceptanceProfileDuration(t *testing.T) {
	t.Parallel()
	root, report, now := validAttestationFixture(t)
	tests := []struct {
		name            string
		mode            string
		profile         string
		duration        int
		requiredMode    string
		requiredProfile string
		wantError       string
	}{
		{name: "test minimum", mode: "soak", profile: AcceptanceProfileTest, duration: 600,
			requiredMode: "soak", requiredProfile: AcceptanceProfileTest},
		{name: "soak satisfies short minimum", mode: "soak", profile: AcceptanceProfileTest, duration: 600,
			requiredMode: "short"},
		{name: "test below minimum", mode: "soak", profile: AcceptanceProfileTest, duration: 599,
			requiredMode: "soak", requiredProfile: AcceptanceProfileTest, wantError: "below test minimum"},
		{name: "release minimum", mode: "soak", profile: AcceptanceProfileRelease, duration: 3600,
			requiredMode: "soak", requiredProfile: AcceptanceProfileRelease},
		{name: "release below minimum", mode: "soak", profile: AcceptanceProfileRelease, duration: 3599,
			requiredMode: "soak", requiredProfile: AcceptanceProfileRelease, wantError: "below release minimum"},
		{name: "profile mismatch", mode: "soak", profile: AcceptanceProfileTest, duration: 3600,
			requiredMode: "soak", requiredProfile: AcceptanceProfileRelease, wantError: "profile"},
		{name: "missing soak profile", mode: "soak", duration: 600,
			requiredMode: "soak", requiredProfile: AcceptanceProfileTest, wantError: "profile"},
		{name: "unknown soak profile", mode: "soak", profile: "unknown", duration: 600,
			requiredMode: "soak", requiredProfile: AcceptanceProfileTest, wantError: "profile"},
		{name: "short rejects profile", mode: "short", profile: AcceptanceProfileTest, duration: 60,
			requiredMode: "short", wantError: "short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneAttestation(t, report)
			candidate.Mode = tt.mode
			candidate.AcceptanceProfile = tt.profile
			candidate.DurationSeconds = tt.duration
			err := VerifyAttestation(root, candidate, VerifyOptions{
				Now: now, ManifestPath: "manifest.json", PolicyManifestPath: "verification.yaml",
				RequiredMode:    tt.requiredMode,
				RequiredProfile: tt.requiredProfile, RequiredPacks: []string{"agent", "iam"},
			})
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
		})
	}
}

func TestGenerateAttestationCanonicalizesAndBindsSource(t *testing.T) {
	t.Parallel()
	root := initDigestRepository(t)
	manifest := testManifest("agent.create", "iam.login")
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "verification.yaml"), []byte("version: 1\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/e2e/attestations"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test/e2e/attestations/safe.log"), []byte("safe evidence"), 0o600))
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	input := validResults(now)
	input.Packs = []PackResult{{ID: "iam", Status: StatusPassed}, {ID: "agent", Status: StatusPassed}}
	input.Capabilities = []CapabilityResult{
		{ID: "iam.login", Status: StatusPassed}, {ID: "agent.create", Status: StatusPassed},
	}
	input.Artifacts = []Artifact{{Kind: "safe_log", Path: "test/e2e/attestations/safe.log"}}

	path, generated, err := GenerateAttestation(root, input, GenerateOptions{
		ManifestPath: "manifest.json", PolicyManifestPath: "verification.yaml",
		OutputDir: "test/e2e/attestations", Now: now,
	})
	require.NoError(t, err)
	require.Equal(t, []PackResult{{ID: "agent", Status: StatusPassed}, {ID: "iam", Status: StatusPassed}}, generated.Packs)
	require.Equal(t, generated.SourceDigest+".json", filepath.Base(path))
	require.Len(t, generated.Artifacts[0].SHA256, 64)
	require.NoError(t, VerifyAttestationFile(root, path, VerifyOptions{
		Now: now, ManifestPath: "manifest.json", PolicyManifestPath: "verification.yaml",
		RequiredPacks: []string{"agent", "iam"},
	}))
}

func TestVerifyRunTopologyRequiresAllRuntimePorts(t *testing.T) {
	t.Parallel()
	valid := validResults(time.Now().UTC()).RunTopology
	tests := []struct {
		name   string
		mutate func(map[string]int)
	}{
		{name: "valid", mutate: func(map[string]int) {}},
		{name: "missing Platform MCP", mutate: func(ports map[string]int) { delete(ports, "platform_mcp") }},
		{name: "missing internal API", mutate: func(ports map[string]int) { delete(ports, "internal_api") }},
		{name: "duplicate internal API", mutate: func(ports map[string]int) { ports["internal_api"] = ports["backend"] }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topology := *valid
			topology.Ports = clonePorts(valid.Ports)
			tt.mutate(topology.Ports)
			err := verifyRunTopology(&topology, &OwnedCleanup{DatabaseDropped: true, LeaseRemoved: true})
			if tt.name == "valid" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestVerifyTopologyIdentityBindsDatabaseToRun(t *testing.T) {
	t.Parallel()
	topology := *validResults(time.Now().UTC()).RunTopology
	topology.DatabaseName = "stratum_e2e_20260730t120102z_1111111111111111"

	require.ErrorContains(t, verifyTopologyIdentity(&topology), "topology identity")
}

func TestGenerateAttestationRejectsCredentialBearingArtifact(t *testing.T) {
	t.Parallel()
	root := initDigestRepository(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), testManifest("agent.create"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "verification.yaml"), []byte("version: 1\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/e2e/attestations"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test/e2e/attestations/unsafe.log"), []byte("password=fixture-secret"), 0o600,
	))
	input := validResults(time.Now().UTC())
	input.Artifacts = []Artifact{{Kind: "safe_log", Path: "test/e2e/attestations/unsafe.log"}}
	_, _, err := GenerateAttestation(root, input, GenerateOptions{
		ManifestPath: "manifest.json", PolicyManifestPath: "verification.yaml",
		OutputDir: "out", Now: time.Now().UTC(),
	})
	require.ErrorContains(t, err, "credential")
}

func TestDecodeAttestationRejectsUnknownFieldsAndCredentialPatterns(t *testing.T) {
	t.Parallel()
	_, err := DecodeAttestation([]byte(`{"schema_version":1,"unexpected":true}`))
	require.ErrorContains(t, err, "unknown field")
	for _, value := range [][]byte{
		[]byte(`{"password":"fixture-secret"}`),
		[]byte(`{"api_key":"fixture-secret"}`),
		[]byte("-----BEGIN PRIVATE KEY-----\nfixture"),
	} {
		require.ErrorContains(t, rejectCredentials(value), "credential")
	}
}

func validAttestationFixture(t *testing.T) (string, Attestation, time.Time) {
	t.Helper()
	root := initDigestRepository(t)
	manifest := testManifest("agent.create")
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0o600))
	policyManifest := []byte("version: 1\npolicy:\n  authority: ci\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "verification.yaml"), policyManifest, 0o600))
	artifact := []byte("safe evidence")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/e2e/attestations"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test/e2e/attestations/safe.log"), artifact, 0o600))
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	sourceDigest, err := LocalSourceDigest(root)
	require.NoError(t, err)
	manifestSum := sha256.Sum256(manifest)
	policyManifestSum := sha256.Sum256(policyManifest)
	artifactSum := sha256.Sum256(artifact)
	report := Attestation{
		SchemaVersion: 1, SourceDigest: sourceDigest, ManifestDigest: hex.EncodeToString(manifestSum[:]),
		PolicyManifestDigest: hex.EncodeToString(policyManifestSum[:]),
		SafeResults: SafeResults{
			TestedGitParent: "HEAD", Browser: Browser{Name: "chromium", Version: "127"},
			Mode: "short", Seed: 42, StartedAt: now.Add(-time.Minute), DurationSeconds: 60, HostClass: "developer",
			Packs:        []PackResult{{ID: "agent", Status: StatusPassed}, {ID: "iam", Status: StatusPassed}},
			Capabilities: []CapabilityResult{{ID: "agent.create", Status: StatusPassed}}, ActionCount: 2,
			SequenceDigest: strings.Repeat("a", 64), Evidence: EvidenceCounts{UI: 2, HTTP: 2, Database: 2, Reconciled: 2},
			Artifacts: []Artifact{{
				Kind: "safe_log", Path: "test/e2e/attestations/safe.log", SHA256: hex.EncodeToString(artifactSum[:]),
			}},
			Cleanup: CleanupResult{Passed: true, ResidualEntityIDs: []string{}}, UnverifiedCapabilities: []string{},
			RiskClassification: "short", Status: StatusPassed,
		},
		ExpiresAt: now.Add(24 * time.Hour),
	}
	return root, report, now
}

func validResults(now time.Time) SafeResults {
	return SafeResults{
		TestedGitParent: "HEAD", Browser: Browser{Name: "chromium", Version: "127"}, Mode: "short", Seed: 42,
		StartedAt: now.Add(-time.Minute), DurationSeconds: 60, HostClass: "developer",
		Packs:        []PackResult{{ID: "agent", Status: StatusPassed}},
		Capabilities: []CapabilityResult{{ID: "agent.create", Status: StatusPassed}}, ActionCount: 1,
		SequenceDigest: strings.Repeat("a", 64), Evidence: EvidenceCounts{UI: 1, HTTP: 1, Database: 1, Reconciled: 1},
		Cleanup: CleanupResult{Passed: true, ResidualEntityIDs: []string{}}, UnverifiedCapabilities: []string{},
		RiskClassification: "short", Status: StatusPassed,
		RunTopology: &RunTopology{
			RunID: "20260730t120102z-a1b2c3d4e5f60718", Host: "127.0.0.1",
			DatabaseName: "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718",
			Ports: map[string]int{
				"frontend": 15174, "backend": 18081, "oauth": 19092, "fixture": 19093,
				"platform_mcp": 18443, "internal_api": 18444,
			},
		},
		OwnedCleanup: &OwnedCleanup{DatabaseDropped: true, LeaseRemoved: true},
	}
}

func clonePorts(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for role, port := range source {
		result[role] = port
	}
	return result
}

func cloneAttestation(t *testing.T, source Attestation) Attestation {
	t.Helper()
	data, err := MarshalCanonical(source)
	require.NoError(t, err)
	copy, err := DecodeAttestation(data)
	require.NoError(t, err)
	return copy
}

func testManifest(ids ...string) []byte {
	capabilities := make([]string, 0, len(ids))
	for _, id := range ids {
		capabilities = append(capabilities, fmt.Sprintf(`{"id":%q,"coverage":"short"}`, id))
	}
	return []byte(`{"version":1,"capabilities":[` + strings.Join(capabilities, ",") + `]}`)
}

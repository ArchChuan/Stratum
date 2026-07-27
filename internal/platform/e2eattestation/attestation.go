package e2eattestation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	StatusPassed    = "passed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
	defaultValidity = 72 * time.Hour
)

type Browser struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type PackResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type CapabilityResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type EvidenceCounts struct {
	UI         int `json:"ui"`
	HTTP       int `json:"http"`
	Database   int `json:"database"`
	Reconciled int `json:"reconciled"`
}
type Artifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
}
type CleanupResult struct {
	Passed            bool     `json:"passed"`
	ResidualEntityIDs []string `json:"residual_entity_ids"`
}

type SafeResults struct {
	TestedGitParent        string             `json:"tested_git_parent"`
	Browser                Browser            `json:"browser"`
	Mode                   string             `json:"mode"`
	Seed                   uint32             `json:"seed"`
	StartedAt              time.Time          `json:"started_at"`
	DurationSeconds        int                `json:"duration_seconds"`
	HostClass              string             `json:"host_class"`
	Packs                  []PackResult       `json:"packs"`
	Capabilities           []CapabilityResult `json:"capabilities"`
	ActionCount            int                `json:"action_count"`
	SequenceDigest         string             `json:"sequence_digest"`
	Evidence               EvidenceCounts     `json:"evidence"`
	Artifacts              []Artifact         `json:"artifacts"`
	Cleanup                CleanupResult      `json:"cleanup"`
	UnverifiedCapabilities []string           `json:"unverified_capabilities"`
	RiskClassification     string             `json:"risk_classification"`
	Status                 string             `json:"status"`
}

type Attestation struct {
	SchemaVersion  int    `json:"schema_version"`
	SourceDigest   string `json:"source_digest"`
	ManifestDigest string `json:"manifest_digest"`
	SafeResults
	ExpiresAt time.Time `json:"expires_at"`
	Signature string    `json:"signature,omitempty"`
}

type GenerateOptions struct {
	ManifestPath, OutputDir string
	Now                     time.Time
	Validity                time.Duration
}
type VerifyOptions struct {
	ManifestPath, Ref, RequiredMode string
	Now                             time.Time
	RequiredPacks                   []string
}

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization\s*[:=]\s*bearer\s+\S+`),
	regexp.MustCompile(`(?i)["']?(authorization)["']?\s*[:=]\s*["']?bearer\s+[^\s,"'}]+`),
	regexp.MustCompile(
		`(?i)["']?(cookie|set-cookie|password|passwd|api[_-]?key|client[_-]?secret)["']?\s*[:=]\s*["']?[^\s,"'}]+`,
	),
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
}

func GenerateAttestation(root string, input SafeResults, options GenerateOptions) (string, Attestation, error) {
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	if options.Validity <= 0 {
		options.Validity = defaultValidity
	}
	sourceDigest, err := LocalSourceDigest(root)
	if err != nil {
		return "", Attestation{}, err
	}
	manifestDigest, err := fileDigest(filepath.Join(root, options.ManifestPath))
	if err != nil {
		return "", Attestation{}, fmt.Errorf("manifest digest: %w", err)
	}
	for i := range input.Artifacts {
		artifactPath, pathErr := safeRepositoryPath(root, input.Artifacts[i].Path)
		if pathErr != nil {
			return "", Attestation{}, pathErr
		}
		content, readErr := os.ReadFile(artifactPath)
		if readErr != nil {
			return "", Attestation{}, fmt.Errorf("read artifact: %w", readErr)
		}
		if err := rejectCredentials(content); err != nil {
			return "", Attestation{}, fmt.Errorf("artifact %q: %w", input.Artifacts[i].Path, err)
		}
		sum := sha256.Sum256(content)
		input.Artifacts[i].SHA256 = hex.EncodeToString(sum[:])
	}
	report := Attestation{
		SchemaVersion:  1,
		SourceDigest:   sourceDigest,
		ManifestDigest: manifestDigest,
		SafeResults:    input,
		ExpiresAt:      options.Now.Add(options.Validity),
	}
	canonicalize(&report)
	data, err := MarshalCanonical(report)
	if err != nil {
		return "", Attestation{}, err
	}
	if err := rejectCredentials(data); err != nil {
		return "", Attestation{}, err
	}
	if options.OutputDir == "" {
		return "", Attestation{}, errors.New("output directory is required")
	}
	outputDir := filepath.Join(root, filepath.FromSlash(options.OutputDir))
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return "", Attestation{}, fmt.Errorf("create output directory: %w", err)
	}
	outputPath := filepath.Join(outputDir, report.SourceDigest+".json")
	if err := os.WriteFile(outputPath, data, 0o600); err != nil {
		return "", Attestation{}, fmt.Errorf("write attestation: %w", err)
	}
	return outputPath, report, nil
}

func VerifyAttestationFile(root, path string, options VerifyOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read attestation: %w", err)
	}
	if err := rejectCredentials(data); err != nil {
		return err
	}
	report, err := DecodeAttestation(data)
	if err != nil {
		return err
	}
	canonical, err := MarshalCanonical(report)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, canonical) {
		return errors.New("attestation is not canonical JSON")
	}
	return VerifyAttestation(root, report, options)
}

func VerifyAttestation(root string, report Attestation, options VerifyOptions) error {
	data, err := MarshalCanonical(report)
	if err != nil {
		return err
	}
	if err := rejectCredentials(data); err != nil {
		return err
	}
	var sourceDigest string
	if options.Ref == "" {
		sourceDigest, err = LocalSourceDigest(root)
	} else {
		sourceDigest, err = CommittedSourceDigest(root, options.Ref)
	}
	if err != nil {
		return err
	}
	if report.SourceDigest != sourceDigest {
		return errors.New("source digest mismatch")
	}
	manifestDigest, err := fileDigest(filepath.Join(root, options.ManifestPath))
	if err != nil {
		return fmt.Errorf("manifest digest: %w", err)
	}
	if report.ManifestDigest != manifestDigest {
		return errors.New("manifest digest mismatch")
	}
	manifest, err := LoadManifest(filepath.Join(root, options.ManifestPath))
	if err != nil {
		return err
	}
	if report.SchemaVersion != 1 {
		return errors.New("unsupported attestation schema version")
	}
	if report.Status != StatusPassed {
		return errors.New("attestation status is not passed")
	}
	if options.RequiredMode != "" && report.Mode != options.RequiredMode {
		return fmt.Errorf("attestation mode %q does not satisfy required mode %q", report.Mode, options.RequiredMode)
	}
	if !report.Cleanup.Passed || len(report.Cleanup.ResidualEntityIDs) != 0 {
		return errors.New("cleanup did not complete without residual entities")
	}
	if len(report.UnverifiedCapabilities) != 0 {
		return errors.New("attestation has unverified capabilities")
	}
	if report.ActionCount <= 0 || report.Evidence.UI <= 0 || report.Evidence.HTTP <= 0 ||
		report.Evidence.Database <= 0 || report.Evidence.Reconciled <= 0 {
		return errors.New("attestation has insufficient actions or evidence")
	}
	if !validDigest(report.SequenceDigest) {
		return errors.New("invalid sequence digest")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	if report.ExpiresAt.IsZero() || !options.Now.Before(report.ExpiresAt) {
		return errors.New("attestation expired")
	}
	packs := make(map[string]string, len(report.Packs))
	for _, pack := range report.Packs {
		packs[pack.ID] = pack.Status
		if pack.Status != StatusPassed {
			return fmt.Errorf("pack %q did not pass", pack.ID)
		}
	}
	for _, required := range options.RequiredPacks {
		if packs[required] != StatusPassed {
			return fmt.Errorf("required pack %q missing or not passed", required)
		}
	}
	capabilities := make(map[string]string, len(report.Capabilities))
	for _, capability := range report.Capabilities {
		if _, duplicate := capabilities[capability.ID]; duplicate {
			return fmt.Errorf("duplicate capability result %q", capability.ID)
		}
		capabilities[capability.ID] = capability.Status
		if capability.Status != StatusPassed {
			return fmt.Errorf("capability %q did not pass", capability.ID)
		}
	}
	for _, capability := range manifest.Capabilities {
		if capability.Coverage != "lower_layer" && capabilities[capability.ID] != StatusPassed {
			return fmt.Errorf("required capability %q missing or not passed", capability.ID)
		}
	}
	for _, artifact := range report.Artifacts {
		artifactPath, pathErr := safeRepositoryPath(root, artifact.Path)
		if pathErr != nil {
			return pathErr
		}
		content, readErr := os.ReadFile(artifactPath)
		if readErr != nil {
			return fmt.Errorf("read artifact %q: %w", artifact.Path, readErr)
		}
		if err := rejectCredentials(content); err != nil {
			return fmt.Errorf("artifact %q: %w", artifact.Path, err)
		}
		sum := sha256.Sum256(content)
		if artifact.SHA256 != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("artifact hash mismatch for %q", artifact.Path)
		}
	}
	return nil
}

func DecodeAttestation(data []byte) (Attestation, error) {
	var report Attestation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return Attestation{}, fmt.Errorf("decode attestation: %w", err)
	}
	if err := ensureAttestationEOF(decoder); err != nil {
		return Attestation{}, err
	}
	return report, nil
}

func DecodeSafeResults(data []byte) (SafeResults, error) {
	var result SafeResults
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return SafeResults{}, fmt.Errorf("decode safe results: %w", err)
	}
	if err := ensureAttestationEOF(decoder); err != nil {
		return SafeResults{}, err
	}
	return result, nil
}

func MarshalCanonical(report Attestation) ([]byte, error) {
	canonicalize(&report)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode canonical attestation: %w", err)
	}
	return append(data, '\n'), nil
}

func canonicalize(report *Attestation) {
	sort.Slice(report.Packs, func(i, j int) bool { return report.Packs[i].ID < report.Packs[j].ID })
	sort.Slice(report.Capabilities, func(i, j int) bool { return report.Capabilities[i].ID < report.Capabilities[j].ID })
	sort.Slice(report.Artifacts, func(i, j int) bool {
		if report.Artifacts[i].Kind == report.Artifacts[j].Kind {
			return report.Artifacts[i].Path < report.Artifacts[j].Path
		}
		return report.Artifacts[i].Kind < report.Artifacts[j].Kind
	})
	sort.Strings(report.Cleanup.ResidualEntityIDs)
	sort.Strings(report.UnverifiedCapabilities)
	if report.Packs == nil {
		report.Packs = []PackResult{}
	}
	if report.Capabilities == nil {
		report.Capabilities = []CapabilityResult{}
	}
	if report.Artifacts == nil {
		report.Artifacts = []Artifact{}
	}
	if report.Cleanup.ResidualEntityIDs == nil {
		report.Cleanup.ResidualEntityIDs = []string{}
	}
	if report.UnverifiedCapabilities == nil {
		report.UnverifiedCapabilities = []string{}
	}
}

func fileDigest(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}
func validDigest(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil
}
func rejectCredentials(data []byte) error {
	for _, pattern := range credentialPatterns {
		if pattern.Match(data) {
			return errors.New("credential pattern detected")
		}
	}
	return nil
}
func safeRepositoryPath(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", errors.New("artifact path must be repository-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("artifact path escapes repository")
	}
	return filepath.Join(root, clean), nil
}
func ensureAttestationEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return errors.New("multiple JSON values")
}

package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ExecutionFingerprint is an immutable snapshot of the configuration used
// for a single agent execution. It is the foundation for offline attribution
// (T9): every dimension that can change between runs is recorded here so the
// attribution engine can slice-and-compare.
type ExecutionFingerprint struct {
	// ModelResolved is the actual model that handled the request after
	// routing and fallback.
	ModelResolved string `json:"model_resolved"`
	// ModelRoutedVia records the fallback chain. Empty slice means direct.
	ModelRoutedVia []string `json:"model_routed_via,omitempty"`
	// PromptVersion identifies the effective prompt, e.g. "system_prompt:v3".
	PromptVersion string `json:"prompt_version"`
	// ConfigVersion fingerprints the effective tunable parameter snapshot at
	// execution time (same source as stratum.params.sha256): ties attribution
	// to the parameter configuration that produced the run.
	ConfigVersion string `json:"config_version,omitempty"`
	// SkillRevisions maps skillID → revision content hash at execution time.
	SkillRevisions map[string]string `json:"skill_revisions,omitempty"`
	// TunableSnapshot captures runtime parameters that affect behaviour:
	// temperature, max_iterations, rerank_enabled, top_k, etc.
	TunableSnapshot map[string]any `json:"tunable_snapshot,omitempty"`
	// ABBucket is the A/B experiment bucket assigned to this execution (0-based).
	// 0 means no experiment.
	ABBucket int `json:"ab_bucket"`
}

// ContentHash returns a deterministic SHA-256 hex digest of the fingerprint.
// Two executions with identical configuration produce the same hash.
func (f ExecutionFingerprint) ContentHash() string {
	// Key order is deterministic: struct fields are serialised in declaration
	// order, maps are sorted before marshaling.
	type wire struct {
		ModelResolved   string            `json:"model_resolved"`
		ModelRoutedVia  []string          `json:"model_routed_via,omitempty"`
		PromptVersion   string            `json:"prompt_version"`
		ConfigVersion   string            `json:"config_version,omitempty"`
		SkillRevisions  map[string]string `json:"skill_revisions,omitempty"`
		TunableSnapshot map[string]any    `json:"tunable_snapshot,omitempty"`
		ABBucket        int               `json:"ab_bucket"`
	}
	w := wire{
		ModelResolved:  f.ModelResolved,
		ModelRoutedVia: f.ModelRoutedVia,
		PromptVersion:  f.PromptVersion,
		ConfigVersion:  f.ConfigVersion,
		SkillRevisions: f.SkillRevisions,
		ABBucket:       f.ABBucket,
	}
	if f.TunableSnapshot != nil {
		w.TunableSnapshot = sortedKeysAny(f.TunableSnapshot)
	}
	if f.SkillRevisions != nil {
		w.SkillRevisions = sortedKeysString(f.SkillRevisions)
	}
	b, _ := json.Marshal(w)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sortedKeysString(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

func sortedKeysAny(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

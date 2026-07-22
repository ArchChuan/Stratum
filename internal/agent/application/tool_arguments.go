package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	toolArgumentsDigestPrefix  = "tool-arguments:v1:sha256:"
	skillRevisionsDigestPrefix = "skill-revisions:v1:sha256:"
)

func CanonicalToolArgumentsDigest(arguments map[string]any) (string, error) {
	return canonicalJSONDigest(toolArgumentsDigestPrefix, arguments)
}

func canonicalSkillRevisionsDigest(revisions map[string]string) (string, error) {
	return canonicalJSONDigest(skillRevisionsDigestPrefix, revisions)
}

func canonicalJSONDigest(prefix string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal canonical JSON: %w", err)
	}
	sum := sha256.Sum256(raw)
	return prefix + hex.EncodeToString(sum[:]), nil
}

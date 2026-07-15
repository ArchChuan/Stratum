package domain

import (
	"errors"
	"fmt"
	"sort"
)

const maxGeneratedCandidates = 64

var allowedParameterFields = map[string]struct{}{
	"model": {}, "temperature": {}, "maxTokens": {},
}

var allowedPromptFields = map[string]struct{}{
	"promptTemplate": {}, "systemPrompt": {},
}

func GenerateParameterPatches(searchSpace map[string][]any) ([]map[string]any, error) {
	if len(searchSpace) == 0 {
		return nil, errors.New("parameter search space required")
	}
	keys := make([]string, 0, len(searchSpace))
	for key, values := range searchSpace {
		if _, ok := allowedParameterFields[key]; !ok {
			return nil, fmt.Errorf("parameter field is not optimizable: %s", key)
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("parameter field has no candidates: %s", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	patches := []map[string]any{{}}
	for _, key := range keys {
		values := searchSpace[key]
		if len(patches)*len(values) > maxGeneratedCandidates {
			return nil, fmt.Errorf("parameter search exceeds %d candidates", maxGeneratedCandidates)
		}
		next := make([]map[string]any, 0, len(patches)*len(values))
		for _, patch := range patches {
			for _, value := range values {
				candidate := make(map[string]any, len(patch)+1)
				for existingKey, existingValue := range patch {
					candidate[existingKey] = existingValue
				}
				candidate[key] = value
				next = append(next, candidate)
			}
		}
		patches = next
	}
	return patches, nil
}

func ValidatePromptPatch(patch map[string]any) error {
	if len(patch) == 0 {
		return errors.New("prompt patch required")
	}
	for key, value := range patch {
		if _, ok := allowedPromptFields[key]; !ok {
			return fmt.Errorf("prompt field is not optimizable: %s", key)
		}
		text, ok := value.(string)
		if !ok || text == "" {
			return fmt.Errorf("prompt field %s must be a non-empty string", key)
		}
	}
	return nil
}

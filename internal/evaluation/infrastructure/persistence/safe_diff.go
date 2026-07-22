package persistence

import (
	"encoding/json"
	"reflect"
	"sort"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

const maxSafeDiffFields = 32

func buildCandidateSafeDiff(parent, candidate map[string]any, parentExists bool) domain.CandidateSafeDiff {
	parent = sanitizeCenterSummary(parent)
	candidate = sanitizeCenterSummary(candidate)
	keys := make([]string, 0, len(parent)+len(candidate))
	seen := make(map[string]struct{}, len(parent)+len(candidate))
	for key := range parent {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range candidate {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	diff := domain.CandidateSafeDiff{
		ChangedFields: make([]string, 0, len(keys)),
		Changes:       make(map[string]domain.SafeFieldChange),
		ParentMissing: !parentExists,
	}
	for _, key := range keys {
		before, beforeOK := parent[key]
		after, afterOK := candidate[key]
		if beforeOK == afterOK && reflect.DeepEqual(before, after) {
			continue
		}
		if len(diff.ChangedFields) >= maxSafeDiffFields {
			break
		}
		diff.ChangedFields = append(diff.ChangedFields, key)
		diff.Changes[key] = domain.SafeFieldChange{Before: before, After: after}
	}
	return diff
}

func sanitizeCenterSummary(summary map[string]any) map[string]any {
	return domain.SanitizeSafeSummary(summary)
}

func parseSanitizedSafeSummary(raw []byte) map[string]any {
	var summary map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &summary) != nil || summary == nil {
		return map[string]any{}
	}
	return domain.SanitizeSafeSummary(summary)
}

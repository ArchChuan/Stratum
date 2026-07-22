package persistence

import (
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
	return sanitizeCenterSummaryMap(summary, 0)
}

func sanitizeCenterSummaryMap(summary map[string]any, depth int) map[string]any {
	result := make(map[string]any, len(summary))
	for key, value := range summary {
		if domain.IsSensitiveSafeSummaryKey(key) {
			continue
		}
		if sanitized, ok := sanitizeCenterSummaryValue(value, depth); ok {
			result[key] = sanitized
		}
	}
	return result
}

func sanitizeCenterSummaryValue(value any, depth int) (any, bool) {
	if depth > 6 {
		return nil, false
	}
	switch typed := value.(type) {
	case nil, bool, float64, int, int32, int64:
		return typed, true
	case string:
		return typed, len(typed) <= 2048
	case []any:
		if len(typed) > 64 {
			return nil, false
		}
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			sanitized, ok := sanitizeCenterSummaryValue(item, depth+1)
			if !ok {
				return nil, false
			}
			result = append(result, sanitized)
		}
		return result, true
	case map[string]any:
		if len(typed) > 64 {
			return nil, false
		}
		result := sanitizeCenterSummaryMap(typed, depth+1)
		if len(typed) > 0 && len(result) == 0 {
			return nil, false
		}
		return result, true
	default:
		return nil, false
	}
}

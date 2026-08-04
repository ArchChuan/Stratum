package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

var (
	ErrInvalidSystemAssistantToolArguments = errors.New("invalid system assistant tool arguments")
	ErrSystemAssistantEvidenceTooLarge     = errors.New("system assistant evidence too large")
)

const (
	SystemAssistantToolSearchOfficialDocs    = "stratum_search_official_docs"
	SystemAssistantToolDiagnoseTenant        = "stratum_diagnose_tenant"
	SystemAssistantToolProposeResourceChange = "stratum_propose_resource_change"
)

// ParseOfficialDocsToolArguments validates and extracts the query argument of
// the system assistant official-docs search tool.
func ParseOfficialDocsToolArguments(args map[string]any) (string, error) {
	raw, err := boundedToolArgumentsJSON(args)
	if err != nil {
		return "", err
	}
	var input struct {
		Query string `json:"query"`
	}
	if err := decodeClosed(raw, &input); err != nil {
		return "", ErrInvalidSystemAssistantToolArguments
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || utf8.RuneCountInString(input.Query) > constants.SystemAssistantQueryMaxRunes {
		return "", ErrInvalidSystemAssistantToolArguments
	}
	return input.Query, nil
}

// ParseDiagnosticToolArguments validates and extracts the diagnostic areas of
// the system assistant tenant-diagnostics tool.
func ParseDiagnosticToolArguments(args map[string]any) ([]DiagnosticArea, error) {
	raw, err := boundedToolArgumentsJSON(args)
	if err != nil {
		return nil, err
	}
	var input struct {
		Areas []DiagnosticArea `json:"areas"`
	}
	if err := decodeClosed(raw, &input); err != nil || len(input.Areas) == 0 || len(input.Areas) > constants.SystemAssistantAreasMaxCount {
		return nil, ErrInvalidSystemAssistantToolArguments
	}
	out := make([]DiagnosticArea, 0, len(input.Areas))
	seen := map[DiagnosticArea]struct{}{}
	for _, area := range input.Areas {
		if !area.Valid() {
			return nil, ErrInvalidSystemAssistantToolArguments
		}
		if _, ok := seen[area]; ok {
			continue
		}
		seen[area] = struct{}{}
		out = append(out, area)
	}
	return out, nil
}

func boundedToolArgumentsJSON(args map[string]any) ([]byte, error) {
	if toolArgumentsSize(args, constants.SystemAssistantToolMaxJSONBytes+1) > constants.SystemAssistantToolMaxJSONBytes {
		return nil, ErrInvalidSystemAssistantToolArguments
	}
	raw, err := json.Marshal(args)
	if err != nil || len(raw) > constants.SystemAssistantToolMaxJSONBytes {
		return nil, ErrInvalidSystemAssistantToolArguments
	}
	return raw, nil
}

func toolArgumentsSize(value any, limit int) int {
	size := 0
	var visit func(any)
	visit = func(item any) {
		if size > limit {
			return
		}
		switch typed := item.(type) {
		case string:
			size += len(typed) + 2
		case map[string]any:
			for key, child := range typed {
				size += len(key) + 4
				visit(child)
			}
		case []any:
			size += 2
			for _, child := range typed {
				size++
				visit(child)
			}
		case []string:
			size += 2
			for _, child := range typed {
				size += len(child) + 3
			}
		default:
			size += 16
		}
	}
	visit(value)
	return size
}

func decodeClosed(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidSystemAssistantToolArguments
	}
	return nil
}

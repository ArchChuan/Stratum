package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

var (
	ErrInvalidSystemAssistantToolArguments = errors.New("invalid system assistant tool arguments")
	ErrSystemAssistantEvidenceTooLarge     = errors.New("system assistant evidence too large")
	sensitiveEvidencePattern               = regexp.MustCompile(`(?i)(password|token|api[_-]?key|authorization|secret)\s*[:=]\s*\S+`)
)

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

func BoundCitations(in []Citation) []Citation {
	if len(in) > constants.SystemAssistantCitationMaxCount {
		in = in[:constants.SystemAssistantCitationMaxCount]
	}
	out := make([]Citation, len(in))
	for i, c := range in {
		c.DocumentID = boundEvidenceString(c.DocumentID)
		c.Title = boundEvidenceString(c.Title)
		c.ProductVersion = boundEvidenceString(c.ProductVersion)
		c.Section = boundEvidenceString(c.Section)
		c.URL = boundEvidenceString(c.URL)
		c.Excerpt = boundEvidenceString(c.Excerpt)
		out[i] = c
	}
	return out
}

func BoundDiagnosticEvidence(in DiagnosticEvidence) DiagnosticEvidence {
	out := in
	if len(in.Facts) > constants.SystemAssistantDiagnosticFactsMaxCount {
		in.Facts = in.Facts[:constants.SystemAssistantDiagnosticFactsMaxCount]
	}
	out.Facts = append([]DiagnosticFact(nil), in.Facts...)
	for i := range out.Facts {
		out.Facts[i].ObjectID = boundEvidenceString(out.Facts[i].ObjectID)
		out.Facts[i].Statement = boundEvidenceString(out.Facts[i].Statement)
		out.Facts[i].Source = boundEvidenceString(out.Facts[i].Source)
		out.Facts[i].SubjectUserID = ""
	}
	if len(in.Gaps) > constants.SystemAssistantDiagnosticGapsMaxCount {
		in.Gaps = in.Gaps[:constants.SystemAssistantDiagnosticGapsMaxCount]
	}
	out.Gaps = append([]EvidenceGap(nil), in.Gaps...)
	for i := range out.Gaps {
		out.Gaps[i].Code = boundEvidenceString(out.Gaps[i].Code)
	}
	if len(in.AreaResults) > constants.SystemAssistantDiagnosticAreaResultsMaxCount {
		in.AreaResults = in.AreaResults[:constants.SystemAssistantDiagnosticAreaResultsMaxCount]
	}
	out.AreaResults = append([]DiagnosticAreaResult(nil), in.AreaResults...)
	for i := range out.AreaResults {
		out.AreaResults[i].Outcome = boundEvidenceString(out.AreaResults[i].Outcome)
	}
	return out
}

func boundEvidenceString(value string) string {
	value = sensitiveEvidencePattern.ReplaceAllString(value, "$1=[REDACTED]")
	runes := []rune(value)
	if len(runes) > constants.SystemAssistantEvidenceFieldMaxRunes {
		runes = runes[:constants.SystemAssistantEvidenceFieldMaxRunes]
	}
	return string(runes)
}

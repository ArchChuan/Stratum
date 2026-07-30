package domain

import (
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/safetext"
)

func BoundCitations(in []Citation) []Citation {
	if len(in) > constants.SystemAssistantCitationMaxCount {
		in = in[:constants.SystemAssistantCitationMaxCount]
	}
	out := make([]Citation, len(in))
	for i, citation := range in {
		citation.DocumentID = boundEvidenceString(citation.DocumentID)
		citation.Title = boundEvidenceString(citation.Title)
		citation.ProductVersion = boundEvidenceString(citation.ProductVersion)
		citation.Section = boundEvidenceString(citation.Section)
		citation.URL = boundEvidenceString(citation.URL)
		citation.Excerpt = boundEvidenceString(citation.Excerpt)
		out[i] = citation
	}
	return out
}

func BoundDiagnosticEvidence(in DiagnosticEvidence) DiagnosticEvidence {
	out := in
	out.Facts = boundedDiagnosticFacts(in.Facts)
	out.Gaps = boundedEvidenceGaps(in.Gaps)
	out.AreaResults = boundedDiagnosticAreaResults(in.AreaResults)
	return out
}

func boundedDiagnosticFacts(in []DiagnosticFact) []DiagnosticFact {
	if len(in) > constants.SystemAssistantDiagnosticFactsMaxCount {
		in = in[:constants.SystemAssistantDiagnosticFactsMaxCount]
	}
	out := append([]DiagnosticFact(nil), in...)
	for i := range out {
		out[i].ObjectID = boundEvidenceString(out[i].ObjectID)
		out[i].Statement = boundEvidenceString(out[i].Statement)
		out[i].Source = boundEvidenceString(out[i].Source)
		out[i].SubjectUserID = ""
	}
	return out
}

func boundedEvidenceGaps(in []EvidenceGap) []EvidenceGap {
	if len(in) > constants.SystemAssistantDiagnosticGapsMaxCount {
		in = in[:constants.SystemAssistantDiagnosticGapsMaxCount]
	}
	out := append([]EvidenceGap(nil), in...)
	for i := range out {
		out[i].Code = boundEvidenceString(out[i].Code)
	}
	return out
}

func boundedDiagnosticAreaResults(in []DiagnosticAreaResult) []DiagnosticAreaResult {
	if len(in) > constants.SystemAssistantDiagnosticAreaResultsMaxCount {
		in = in[:constants.SystemAssistantDiagnosticAreaResultsMaxCount]
	}
	out := append([]DiagnosticAreaResult(nil), in...)
	for i := range out {
		out[i].Outcome = boundEvidenceString(out[i].Outcome)
	}
	return out
}

func boundEvidenceString(value string) string {
	value = safetext.RedactCredentials(value)
	runes := []rune(value)
	if len(runes) > constants.SystemAssistantEvidenceFieldMaxRunes {
		runes = runes[:constants.SystemAssistantEvidenceFieldMaxRunes]
	}
	return string(runes)
}

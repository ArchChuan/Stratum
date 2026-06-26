package pipeline

import "fmt"

const enrichmentPrompt = `Analyze this conversation message and extract structured metadata.

Return valid JSON with exactly these fields:
{
  "entities": [{"name": "...", "type": "person|product|concept|location|org", "confidence": 0.0-1.0}],
  "importance": 0.0-1.0,
  "token_estimate": 123,
  "keywords": ["keyword1", "keyword2"]
}

Rules:
- importance: 0.9+ for decisions/commitments, 0.7-0.9 for facts/preferences, 0.3-0.7 for context, <0.3 for filler
- entities: only extract clearly named entities with confidence >= 0.6
- keywords: 3-5 most relevant terms for future retrieval
- token_estimate: approximate token count of the message content

Message (role: %s):
%s`

const summaryPrompt = `Summarize this conversation concisely, preserving key decisions, facts, and action items. Be brief but complete.

Conversation:
%s`

func formatEnrichmentPrompt(tmpl, role, content string) string {
	if tmpl == "" {
		tmpl = enrichmentPrompt
	}
	return fmt.Sprintf(tmpl, role, content)
}

func formatSummaryPrompt(tmpl, conversation string) string {
	if tmpl == "" {
		tmpl = summaryPrompt
	}
	return fmt.Sprintf(tmpl, conversation)
}

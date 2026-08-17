package pipeline

import (
	"fmt"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func formatEnrichmentPrompt(tmpl, role, content string) string {
	if tmpl == "" {
		tmpl = constants.MemoryEnrichDefaultPrompt
	}
	return fmt.Sprintf(tmpl, role, content)
}

func formatSummaryPrompt(tmpl, conversation string) string {
	if tmpl == "" {
		tmpl = constants.MemorySummaryDefaultPrompt
	}
	return fmt.Sprintf(tmpl, conversation)
}

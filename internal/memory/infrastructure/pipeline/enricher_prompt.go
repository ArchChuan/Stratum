package pipeline

import (
	"fmt"
)

// formatEnrichmentPrompt 渲染富化系统提示词；调用方保证 tmpl 非空
// （memory.enrich_prompt 未配置时 fail-closed，禁止空模板静默调用 LLM）。
func formatEnrichmentPrompt(tmpl, role, content string) string {
	return fmt.Sprintf(tmpl, role, content)
}

// formatSummaryPrompt 渲染会话摘要系统提示词；调用方保证 tmpl 非空。
func formatSummaryPrompt(tmpl, conversation string) string {
	return fmt.Sprintf(tmpl, conversation)
}

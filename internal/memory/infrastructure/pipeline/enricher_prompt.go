package pipeline

import "fmt"

const enrichmentPrompt = `分析以下对话消息，提取结构化元数据。

只输出符合以下格式的 JSON，不加任何说明或 markdown 标记：
{
  "entities": [{"name": "...", "type": "person|product|concept|location|org", "confidence": 0.0-1.0}],
  "importance": 0.0-1.0,
  "token_estimate": 数字,
  "keywords": ["关键词1", "关键词2"],
  "work_context": ["当前工作、任务或约束"],
  "personal_context": ["当前明确表达的个人偏好或状态"],
  "top_of_mind": ["当前最关注的事项"]
}

规则：
- importance 评分：0.9+ 决策/承诺；0.7-0.9 具体事实/偏好；0.3-0.7 一般上下文；<0.3 无实质内容（问候/感谢/简单确认）
- entities：只提取置信度 >= 0.6 的具名实体
- keywords：3-5 个最有检索价值的词语
- token_estimate：消息内容的 token 数近似值
- 三个 context 数组仅保留当前仍活跃、明确且必要的短句；每组最多 8 项，每项不超过 240 字；不要输出密码、令牌、密钥或原始整段消息

消息（角色：%s）：
%s`

const summaryPrompt = `简洁总结以下对话，保留关键决策、确认的事实和待办事项。要求简短但完整，使用中文。

对话内容：
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

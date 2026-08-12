package domain

import "fmt"

// 本文档文件是机制面的 embedded 种子（代码轨默认）：DB model_profiles 缺档时
// 回退的现状值，与改造前硬编码行为完全一致。消费路径改造后，各管线不再持有
// 自己的模板常量，统一经种子/DB 基线取用。

// SeedCompactionPrompt 对应原 agent capability/history_compactor.go 的
// compactionSystemPrompt。
const SeedCompactionPrompt = "你是对话历史压缩器。请把以下对话压成不超过 500 字的要点摘要，" +
	"以第三人称客观记录：保留关键事实、已达成的决定、以及尚未解决的问题；" +
	"剔除寒暄与冗余细节。只输出摘要正文，不要任何前后缀。"

// SeedMemoryEnrichmentPrompt 对应原 pipeline/enricher_prompt.go 的
// enrichmentPrompt。
const SeedMemoryEnrichmentPrompt = `分析以下对话消息，提取结构化元数据。

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

// SeedMemorySummaryPrompt 对应原 pipeline/enricher_prompt.go 的 summaryPrompt。
const SeedMemorySummaryPrompt = `简洁总结以下对话，保留关键决策、确认的事实和待办事项。要求简短但完整，使用中文。

对话内容：
%s`

// SeedMemorySummarizePrompt 对应原 workers/history_summarizer.go 的
// SummarizeHistory 提示词。
const SeedMemorySummarizePrompt = "Summarize this bounded period of user history. Preserve decisions, goals, preferences, and durable context; omit secrets and raw payloads.\n\n"

// SeedMemoryExtractionPrompt 对应原 pipeline/llm_extractor.go 的提取模板。
// 与原文一致含 %s/%d 占位（userID/agentID/maxFacts），消费方 fmt.Sprintf 使用。
const SeedMemoryExtractionPrompt = `你是一个长期记忆提取助手，负责从对话中提取关于用户（%s）的有价值事实，供 AI 助手（%s）在未来对话中使用。

提取规则（严格执行）：
- 只提取用户明确陈述、确认或展现的事实
- 不提取：用户的提问、问候语、AI 助手的回复内容、工具调用的输出
- 不提取泛化描述（如"用户提到了某件事"），只提取具体事实
- 优先精确性：「用户偏好在 VS Code 中使用暗色主题」优于「用户有主题偏好」
- 最多提取 %d 条事实；宁少勿滥，低价值事实直接忽略

fact_type 分类：
- preference：用户的喜好、偏好、习惯
- skill：用户掌握的技能或专业知识
- event：已发生的具体事件（过去时）
- state：用户当前的状态或处境
- relationship：用户与某人/某组织的关系
- other：不属于以上分类的陈述性事实

只输出 JSON 数组，不加任何说明或 markdown 标记：
[{"content":"...","importance":0.0-1.0,"fact_type":"...","confidence":0.0-1.0,"entities":["实体名"]}]`

// SeedMemorySupersedePrompt 对应原 workers/llm_superseder.go 的判断模板。
// 含 %s/%s 占位（oldFact/newFact），消费方 fmt.Sprintf 使用。
const SeedMemorySupersedePrompt = `判断新事实是否应该取代旧事实。

旧事实：%s
新事实：%s

判断标准：
- 如果新事实是对旧事实的更新、纠正或推翻，则应取代（supersedes: true）
- 如果两者描述不同方面或可以并存，则不取代（supersedes: false）
- 如果新事实只是旧事实的子集或更模糊的表达，则不取代

只输出 JSON，不加任何说明：
{"supersedes": true/false, "reason": "简短说明"}`

// DefaultBaseline 返回种子档位基线（现状硬编码值）。无 DB 档案时全系统
// 回退此值，行为与改造前一致。六键必须与消费路径逐一对应，禁止漏键。
func DefaultBaseline() Baseline {
	return Baseline{
		Prompts: BaselinePrompts{
			MemoryExtraction: SeedMemoryExtractionPrompt,
			MemorySummary:    SeedMemorySummaryPrompt,
			MemoryEnrichment: SeedMemoryEnrichmentPrompt,
			MemorySummarize:  SeedMemorySummarizePrompt,
			MemorySupersede:  SeedMemorySupersedePrompt,
			Compaction:       SeedCompactionPrompt,
		},
		Models: BaselineModels{},
		Recall: BaselineRecall{},
	}
}

// FormatCompaction 按种子模板渲染压缩提示词（含对话正文）。
func FormatCompaction(tmpl, conversation string) string {
	return tmpl + "\n" + conversation
}

// FormatEnrichment 按模板渲染富化提示词（角色 + 内容），tmpl 空时回退种子。
func FormatEnrichment(tmpl, role, content string) string {
	if tmpl == "" {
		tmpl = SeedMemoryEnrichmentPrompt
	}
	return fmt.Sprintf(tmpl, role, content)
}

// FormatSummary 按模板渲染总结提示词，tmpl 空时回退种子。
func FormatSummary(tmpl, conversation string) string {
	if tmpl == "" {
		tmpl = SeedMemorySummaryPrompt
	}
	return fmt.Sprintf(tmpl, conversation)
}

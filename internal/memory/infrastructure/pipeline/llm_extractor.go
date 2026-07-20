package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// LLMExtractor adapts LLMClient to memport.LLMExtractor.
type LLMExtractor struct{ client LLMClient }

func NewLLMExtractor(client LLMClient) *LLMExtractor {
	return &LLMExtractor{client: client}
}

func (e *LLMExtractor) ExtractFacts(ctx context.Context, userID, agentID string, message string) ([]*memport.ExtractedFact, error) {
	system := fmt.Sprintf(`你是一个长期记忆提取助手，负责从对话中提取关于用户（%s）的有价值事实，供 AI 助手（%s）在未来对话中使用。

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
[{"content":"...","importance":0.0-1.0,"fact_type":"...","confidence":0.0-1.0,"entities":["实体名"]}]`, userID, agentID, constants.MemoryMaxFactsPerExtraction)
	resp, err := e.client.Complete(ctx, &memport.CompletionRequest{
		Messages: []memport.CompletionMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: message},
		},
		MaxTokens: constants.MemoryExtractLLMMaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("llm extract: %w", err)
	}
	raw := resp.Content
	start := strings.Index(raw, "[")
	if start == -1 {
		return nil, fmt.Errorf("parse extracted facts: no JSON array in response")
	}
	body := raw[start:]
	var facts []*memport.ExtractedFact
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&facts); err != nil {
		// Token limit may have truncated the JSON mid-object; recover by closing at the last complete item.
		if recovered := recoverTruncatedArray(body); recovered != "" {
			if err2 := json.Unmarshal([]byte(recovered), &facts); err2 == nil {
				return facts, nil
			}
		}
		return nil, fmt.Errorf("parse extracted facts: %w", err)
	}
	return facts, nil
}

// recoverTruncatedArray finds the last complete JSON object in a truncated array and closes it.
func recoverTruncatedArray(s string) string {
	last := strings.LastIndex(s, "},")
	if last == -1 {
		last = strings.LastIndex(s, "}")
	} else {
		last++ // include the }
	}
	if last == -1 {
		return ""
	}
	return s[:last+1] + "]"
}

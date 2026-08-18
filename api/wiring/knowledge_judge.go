package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

type knowledgeJudge struct {
	completer llmgatewaydomain.LLMCompleter
	model     string
	timeout   time.Duration
	metrics   observability.MetricsProvider
}

func (j knowledgeJudge) judgeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := j.timeout
	if timeout <= 0 {
		timeout = constants.KnowledgeJudgeTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (j knowledgeJudge) JudgeSufficiency(ctx context.Context, query, evidence string) (knowledgeport.SufficiencyVerdict, error) {
	ctx, cancel := j.judgeContext(ctx)
	defer cancel()
	resp, err := j.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:     j.model,
		MaxTokens: constants.KnowledgeJudgeMaxTokens,
		ResponseFormat: &llmgatewaydomain.ResponseFormat{
			Type: "json_object",
		},
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: "你是严谨的证据充分性法官。只输出 JSON，不输出其他内容。"},
			{Role: "user", Content: fmt.Sprintf(
				"判断给定证据是否足以支撑回答该问题。证据不足时宁可判不足，禁止猜测。\n\nQuestion:\n%s\n\nEvidence:\n%s\n\n输出 JSON：{\"sufficient\": true|false}。",
				query, truncateRunes(evidence, constants.KnowledgeJudgeMaxEvidenceRunes),
			)},
		},
	})
	if err != nil {
		j.record("error")
		return "", fmt.Errorf("knowledge judge: sufficiency: %w", err)
	}
	var parsed struct {
		Sufficient *bool `json:"sufficient"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		j.record("error")
		return "", fmt.Errorf("knowledge judge: parse sufficiency: %w", err)
	}
	if parsed.Sufficient == nil {
		j.record("error")
		return "", fmt.Errorf("knowledge judge: parse sufficiency: missing sufficient field")
	}
	j.record("ok")
	if *parsed.Sufficient {
		return knowledgeport.SufficiencySufficient, nil
	}
	return knowledgeport.SufficiencyInsufficient, nil
}

func (j knowledgeJudge) JudgeFaithfulness(ctx context.Context, answer, evidence string) ([]knowledgeport.FaithfulnessVerdict, error) {
	ctx, cancel := j.judgeContext(ctx)
	defer cancel()
	resp, err := j.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:     j.model,
		MaxTokens: constants.KnowledgeJudgeMaxTokens,
		ResponseFormat: &llmgatewaydomain.ResponseFormat{
			Type: "json_object",
		},
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: "你是严谨的事实支撑法官。只输出 JSON，不输出其他内容。"},
			{Role: "user", Content: fmt.Sprintf(
				"对答案中的每个事实性陈述（claim），依据给定证据判定是否被支撑。证据不能支撑的判 UNSUPPORTED。\n\nAnswer:\n%s\n\nEvidence:\n%s\n\n输出 JSON：{\"claims\":[{\"text\":\"<claim>\",\"verdict\":\"SUPPORTED|UNSUPPORTED\"}]}。",
				answer, truncateRunes(evidence, constants.KnowledgeJudgeMaxEvidenceRunes),
			)},
		},
	})
	if err != nil {
		j.record("error")
		return nil, fmt.Errorf("knowledge judge: faithfulness: %w", err)
	}
	var parsed struct {
		Claims []struct {
			Text    string `json:"text"`
			Verdict string `json:"verdict"`
		} `json:"claims"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		j.record("error")
		return nil, fmt.Errorf("knowledge judge: parse faithfulness: %w", err)
	}
	verdicts := make([]knowledgeport.FaithfulnessVerdict, 0, len(parsed.Claims))
	for _, c := range parsed.Claims {
		v := knowledgeport.FaithfulnessUnsupported
		if c.Verdict == string(knowledgeport.FaithfulnessSupported) {
			v = knowledgeport.FaithfulnessSupported
		}
		verdicts = append(verdicts, v)
	}
	j.record("ok")
	return verdicts, nil
}

func (j knowledgeJudge) JudgeContradiction(ctx context.Context, query, evidence string) (bool, error) {
	ctx, cancel := j.judgeContext(ctx)
	defer cancel()
	resp, err := j.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:     j.model,
		MaxTokens: constants.KnowledgeJudgeMaxTokens,
		ResponseFormat: &llmgatewaydomain.ResponseFormat{
			Type: "json_object",
		},
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: "你是严谨的矛盾检测法官。只输出 JSON，不输出其他内容。"},
			{Role: "user", Content: fmt.Sprintf(
				"判断给定证据片段之间是否存在实质性矛盾（同一事实有互斥说法）。只输出结论，不解释。\n\nQuestion:\n%s\n\nEvidence:\n%s\n\n输出 JSON：{\"contradiction\": true|false}。",
				query, truncateRunes(evidence, constants.KnowledgeJudgeMaxEvidenceRunes),
			)},
		},
	})
	if err != nil {
		j.record("error")
		return false, fmt.Errorf("knowledge judge: contradiction: %w", err)
	}
	var parsed struct {
		Contradiction bool `json:"contradiction"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		j.record("error")
		return false, fmt.Errorf("knowledge judge: parse contradiction: %w", err)
	}
	j.record("ok")
	return parsed.Contradiction, nil
}

func (j knowledgeJudge) record(status string) {
	if j.metrics != nil {
		j.metrics.IncKnowledgeJudge(j.model, status)
	}
}

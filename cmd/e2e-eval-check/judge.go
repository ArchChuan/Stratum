package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// judgeResult is the structured judge verdict.
type judgeResult struct {
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// judgeClient calls an OpenAI-compatible /chat/completions endpoint to judge
// whether a produced output satisfies a criteria. The judge API key travels
// only in the Authorization header — never in URLs, logs, or error bodies.
type judgeClient struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

// newJudgeClient normalizes the base URL so both https://api.openai.com and
// https://api.openai.com/v1 work: a trailing /v1 is stripped so the request
// path never double-appends.
func newJudgeClient(cfg *judgeConfig, apiKey string) *judgeClient {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return &judgeClient{
		baseURL: baseURL,
		model:   cfg.Model,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: DefaultHTTPTimeout},
	}
}

// Judge asks the LLM to grade output against criteria, fail-closed on any
// malformed response: a broken judge is infra, never a silent pass.
func (j *judgeClient) Judge(ctx context.Context, spec judgeSpec, output string) (judgeResult, error) {
	body, err := json.Marshal(map[string]any{
		"model": j.model,
		"messages": []map[string]string{
			{"role": "system", "content": judgeSystemPrompt},
			{"role": "user", "content": fmt.Sprintf("标准：%s\n\n候选输出：\n%s", spec.Criteria, output)},
		},
		"temperature": 0,
	})
	if err != nil {
		return judgeResult{}, fmt.Errorf("encode judge request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return judgeResult{}, fmt.Errorf("build judge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if j.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+j.apiKey)
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return judgeResult{}, &infraError{fmt.Errorf("judge request: %w", err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return judgeResult{}, &infraError{fmt.Errorf("read judge response: %w", err)}
	}
	if resp.StatusCode != http.StatusOK {
		return judgeResult{}, &infraError{fmt.Errorf("judge HTTP %d: %s", resp.StatusCode, data)}
	}
	return parseJudgeResponse(data)
}

// parseJudgeResponse decodes the LLM verdict, fail-closed on any malformed
// payload: no choices, a non-JSON verdict, or a score outside [0,100] are all
// infrastructure failures (exit 2), never a silent pass.
func parseJudgeResponse(data []byte) (judgeResult, error) {
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return judgeResult{}, &infraError{fmt.Errorf("decode judge response: %w", err)}
	}
	if len(payload.Choices) == 0 {
		return judgeResult{}, &infraError{errors.New("judge response has no choices")}
	}
	content := strings.TrimSpace(strings.Trim(payload.Choices[0].Message.Content, "`"))
	content = strings.TrimPrefix(content, "json")
	var verdict judgeResult
	if err := json.Unmarshal([]byte(content), &verdict); err != nil {
		return judgeResult{}, &infraError{fmt.Errorf("decode judge verdict %q: %w", content, err)}
	}
	if verdict.Score < 0 || verdict.Score > 100 {
		return judgeResult{}, &infraError{fmt.Errorf("judge score %g out of [0,100]", verdict.Score)}
	}
	return verdict, nil
}

const judgeSystemPrompt = `你是评测判题员。根据判定标准判断候选输出是否通过。
标准未提及的维度不要臆测。只输出 JSON：{"passed": true/false, "score": 0-100 的整数, "reason": "一句话理由"}`

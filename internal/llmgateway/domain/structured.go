package domain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrStructuredOutputFailed 表示结构化输出经过全部带错重试后仍 0 条通过校验。
// 调用方必须保留失败语义（MarkFailed/DLQ），禁止静默标记 completed。
var ErrStructuredOutputFailed = errors.New("structured output: all candidates failed validation")

// Completer 是结构化重试内核依赖的最小完成接口（单次非流式）。
// memory pipeline 的 LLMClient / workers 的 TenantLLMClient 均为其别名。
type Completer interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}

// FieldError 由消费方校验错误实现（如 memory 的 ValidationError），
// 供白名单降级摘要取字段名。实现必须 nil-safe（Field 方法对 nil 接收者
// 返回空串，防 typed-nil 经 errors.As 命中后解引用 panic）。
type FieldError interface {
	Field() string
}

// FailureSummary 是降级日志的白名单摘要：只记计数与字段名，禁止原始模型输出
// 与校验违规值（防 PII/原文泄露）。
type FailureSummary struct {
	Attempts      int
	ParseErrors   int
	InvalidFields map[string]int
}

// Record 累计单次失败：FieldError 记字段名（白名单），其余记 parse 错误数。
func (s *FailureSummary) Record(err error) {
	var fe FieldError
	if errors.As(err, &fe) {
		if s.InvalidFields == nil {
			s.InvalidFields = make(map[string]int)
		}
		s.InvalidFields[fe.Field()]++
		return
	}
	s.ParseErrors++
}

// FieldNames 返回已排序的字段名列表（确定性日志输出）。
func (s *FailureSummary) FieldNames() []string {
	names := make([]string, 0, len(s.InvalidFields))
	for f := range s.InvalidFields {
		names = append(names, f)
	}
	sort.Strings(names)
	return names
}

// StructuredOutputError 是带白名单摘要的 typed error。Error() 只含字段名与计数，
// 不含违规值；Unwrap 到 ErrStructuredOutputFailed 供 errors.Is 判定。
type StructuredOutputError struct {
	Kind    string
	Summary FailureSummary
}

func (e *StructuredOutputError) Error() string {
	// %w 仅 fmt.Errorf 支持；取 .Error() 嵌入哨兵消息并保持 Error() 返回 string。
	return fmt.Errorf("%w: %s (attempts=%d, parse_errors=%d, invalid_fields=%s)",
		ErrStructuredOutputFailed, e.Kind, e.Summary.Attempts, e.Summary.ParseErrors,
		strings.Join(e.Summary.FieldNames(), ",")).Error()
}

func (e *StructuredOutputError) Unwrap() error { return ErrStructuredOutputFailed }

// correctionMessage 把校验错误上下文构造成 system role 消息丢回模型自修复。
// 用户约束：重试必须告诉模型错误在哪里（具体字段/值/原因），而非简单重试。
// 用 system role 而非 user role，避免模型把校验错误当用户陈述污染上下文。
func correctionMessage(correction string) Message {
	return Message{Role: "system", Content: "{correction: " + correction + "}"}
}

// cloneReq 浅拷贝请求并复制 Messages 切片，避免带错重试追加 correction 时
// 原地写共享底层数组污染调用方请求。response_format 设于副本上。
func cloneReq(req *CompletionRequest) *CompletionRequest {
	cloned := *req
	cloned.Messages = append([]Message(nil), req.Messages...)
	return &cloned
}

// StructuredRetryLoop 是结构化 JSON 输出的非泛型带错重试内核（stdlib only，
// 不 import zap）：
//  1. 在请求副本上设 response_format=json_object，provider 保证合法 JSON；
//  2. attempt 返回 nil = 本次解析+校验通过 → 返回原始输出字符串；
//     attempt 返回非 nil = 失败 → 白名单记录 + 构造带错 correction（system role）
//     丢回模型，最多重试 maxRetries 次；
//  3. provider 硬错误（网络/4xx/5xx）fail-fast，不消耗重试；
//  4. 全部失败 → 返回 *StructuredOutputError（白名单摘要），消费方外壳负责
//     记录降级 WARN（llmdomain 不 import zap）。
//
// kind 是日志阶段名（extract_facts|enrich|supersede），白名单枚举。
// 消费方保留薄泛型 CompleteStructured[T] 外壳调用本内核，JSON 解析、逐条校验、
// 部分成功语义（≥1 通过返回子集）留本地。
func StructuredRetryLoop(
	ctx context.Context,
	client Completer,
	req *CompletionRequest,
	maxRetries int,
	kind string,
	attempt func(content string) error,
) (string, error) {
	req = cloneReq(req)
	req.ResponseFormat = JSONObject()

	var summary FailureSummary
	for try := 0; try <= maxRetries; try++ {
		summary.Attempts = try + 1
		resp, err := client.Complete(ctx, req)
		if err != nil {
			// provider 硬错误 fail-fast：重试无法自愈，且严格端点 400 已由
			// 网关能力门控拦截，这里出现的错误向上传播即可。
			return "", fmt.Errorf("llm complete (%s): %w", kind, err)
		}
		if aerr := attempt(resp.Content); aerr != nil {
			summary.Record(aerr)
			req.Messages = append(req.Messages, correctionMessage(aerr.Error()))
			continue
		}
		return resp.Content, nil
	}
	return "", &StructuredOutputError{Kind: kind, Summary: summary}
}

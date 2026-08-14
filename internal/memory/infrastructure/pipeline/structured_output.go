package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// ErrStructuredExtractionFailed 表示结构化输出经过全部带错重试后仍 0 条通过校验。
// 调用方必须保留失败语义（MarkFailed/DLQ），禁止静默标记 completed。
var ErrStructuredExtractionFailed = errors.New("memory structured output: all candidates failed validation")

// JSONObject 返回 OpenAI-compatible JSON mode 的 response_format。
// provider 保证返回合法 JSON，服务端校验退化为语义层。
func JSONObject() *memport.ResponseFormat {
	return &memport.ResponseFormat{Type: "json_object"}
}

// CorrectionMessage 把校验错误上下文构造成 system role 消息丢回模型自修复。
// 用户约束：重试必须告诉模型错误在哪里（具体字段/值/原因），而非简单重试。
// 用 system role 而非 user role，避免模型把校验错误当用户陈述污染上下文。
func CorrectionMessage(correction string) memport.CompletionMessage {
	return memport.CompletionMessage{Role: "system", Content: "{correction: " + correction + "}"}
}

// structuredFailureSummary 是降级日志的白名单摘要：只记计数与字段名，
// 禁止原始模型输出与校验违规值（防 PII/原文泄露）。
type structuredFailureSummary struct {
	attempts    int
	parseErrors int
	invalidFlds map[string]int
}

// record 累计单次失败：ValidationError 记字段名（白名单），其余记 parse 错误数。
// ve == nil 守卫防 typed-nil：调用方若把 (*ValidationError)(nil) 包进 error 接口，
// errors.As 会命中类型但值为 nil，直接解引用 panic——此处兜底记 parse 错误。
func (s *structuredFailureSummary) record(err error) {
	var ve *memport.ValidationError
	if errors.As(err, &ve) && ve != nil {
		if s.invalidFlds == nil {
			s.invalidFlds = make(map[string]int)
		}
		s.invalidFlds[ve.FieldName]++
		return
	}
	s.parseErrors++
}

// fieldNames 返回已排序的字段名列表（确定性日志输出）。
func (s *structuredFailureSummary) fieldNames() []string {
	names := make([]string, 0, len(s.invalidFlds))
	for f := range s.invalidFlds {
		names = append(names, f)
	}
	sort.Strings(names)
	return names
}

// logStructuredDegraded 记录结构化输出降级 WARN，仅含白名单字段。
// logger 可能为 nil（测试/降级启动），nil 安全。
func logStructuredDegraded(logger *zap.Logger, kind string, s structuredFailureSummary) {
	if logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("stage", "memory."+kind+".structured_degraded"),
		zap.Int("attempts", s.attempts),
		zap.Int("parse_errors", s.parseErrors),
	}
	for _, f := range s.fieldNames() {
		fields = append(fields, zap.Int("invalid_field_"+f, s.invalidFlds[f]))
	}
	logger.Warn("memory.structured.degraded", fields...)
}

// cloneReq 浅拷贝请求并复制 Messages 切片，避免带错重试追加 correction 时
// 原地写共享底层数组污染调用方请求。response_format 设于副本上。
func cloneReq(req *memport.CompletionRequest) *memport.CompletionRequest {
	cloned := *req
	cloned.Messages = append([]memport.CompletionMessage(nil), req.Messages...)
	return &cloned
}

// CompleteStructured 是结构化 JSON 输出的统一入口（memory 三个直接 JSON 调用点共用）：
//  1. 请求设 response_format=json_object，provider 保证合法 JSON；
//  2. 解析失败 → 构造带错 correction（system role）丢回模型，最多重试
//     MemoryMaxStructuredRetries 次；
//  3. provider 硬错误（网络/4xx/5xx）fail-fast，不消耗重试；
//  4. 全部失败 → logStructuredDegraded（白名单）+ typed error
//     ErrStructuredExtractionFailed（调用方保留 MarkFailed/DLQ 语义）。
//
// kind 是日志阶段名（extract_facts|enrich|supersede），白名单枚举。
func CompleteStructured[T any](
	ctx context.Context,
	client memport.Completer,
	req *memport.CompletionRequest,
	parse func(string) (T, error),
	validate func(T) error,
	logger *zap.Logger,
	kind string,
) (T, error) {
	var zero T
	req = cloneReq(req)
	req.ResponseFormat = JSONObject()

	var summary structuredFailureSummary
	for attempt := 0; attempt <= constants.MemoryMaxStructuredRetries; attempt++ {
		summary.attempts = attempt + 1
		resp, err := client.Complete(ctx, req)
		if err != nil {
			// provider 硬错误 fail-fast：重试无法自愈，且严格端点 400 已由
			// 网关能力门控拦截，这里出现的错误向上传播即可。
			return zero, fmt.Errorf("llm complete (%s): %w", kind, err)
		}
		out, err := parse(resp.Content)
		if err != nil {
			summary.record(err)
			req.Messages = append(req.Messages, CorrectionMessage(err.Error()))
			continue
		}
		if verr := validate(out); verr != nil {
			summary.record(verr)
			req.Messages = append(req.Messages, CorrectionMessage(verr.Error()))
			continue
		}
		return out, nil
	}
	logStructuredDegraded(logger, kind, summary)
	return zero, fmt.Errorf("%w: %s (attempts=%d, parse_errors=%d, invalid_fields=%s)",
		ErrStructuredExtractionFailed, kind, summary.attempts, summary.parseErrors,
		strings.Join(summary.fieldNames(), ","))
}

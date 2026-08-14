package pipeline

import (
	"context"
	"errors"

	"go.uber.org/zap"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// CompleteStructured 是结构化 JSON 输出的统一泛型外壳（memory 三个直接 JSON
// 调用点共用）。重试/带错 correction/白名单摘要由 llmdomain.StructuredRetryLoop
// 内核承担；本外壳只做类型化解析、逐条校验与部分成功语义：
//  1. 解析失败 → 交给内核走带错重试；
//  2. 校验失败 → 交给内核走带错重试；
//  3. 全部失败 → 记录降级 WARN（白名单）+ 透传 *llmdomain.StructuredOutputError
//     （调用方保留 MarkFailed/DLQ 语义）。
//
// kind 是日志阶段名（extract_facts|enrich|supersede），白名单枚举。
func CompleteStructured[T any](
	ctx context.Context,
	client llmdomain.Completer,
	req *llmdomain.CompletionRequest,
	parse func(string) (T, error),
	validate func(T) error,
	logger *zap.Logger,
	kind string,
) (T, error) {
	var zero T
	var parsed T
	_, err := llmdomain.StructuredRetryLoop(ctx, client, req, constants.MemoryMaxStructuredRetries, kind,
		func(content string) error {
			v, perr := parse(content)
			if perr != nil {
				return perr
			}
			if verr := validate(v); verr != nil {
				return verr
			}
			parsed = v
			return nil
		})
	if err != nil {
		var soe *llmdomain.StructuredOutputError
		if errors.As(err, &soe) {
			logStructuredDegraded(logger, kind, soe.Summary)
		}
		return zero, err
	}
	return parsed, nil
}

// logStructuredDegraded 记录结构化输出降级 WARN，仅含白名单字段。
// logger 可能为 nil（测试/降级启动），nil 安全。
func logStructuredDegraded(logger *zap.Logger, kind string, s llmdomain.FailureSummary) {
	if logger == nil {
		return
	}
	fields := []zap.Field{
		zap.String("stage", "memory."+kind+".structured_degraded"),
		zap.Int("attempts", s.Attempts),
		zap.Int("parse_errors", s.ParseErrors),
	}
	for _, f := range s.FieldNames() {
		fields = append(fields, zap.Int("invalid_field_"+f, s.InvalidFields[f]))
	}
	logger.Warn("memory.structured.degraded", fields...)
}

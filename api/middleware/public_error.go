package middleware

import (
	"errors"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

const CodeSystemAssistantModelUnavailable = "SYSTEM_ASSISTANT_MODEL_UNAVAILABLE"

type PublicErrorDescriptor struct {
	Message string
	Code    string
}

func DescribePublicError(err error, status int) PublicErrorDescriptor {
	if errors.Is(err, agentdomain.ErrAssistantModelUnavailable) {
		return PublicErrorDescriptor{
			Message: "租户尚未配置平台助手模型",
			Code:    CodeSystemAssistantModelUnavailable,
		}
	}
	// ErrUpstreamRequestFailed 的 wrap 链含内部 BaseURL/上游响应细节，
	// 只对客户端暴露固定消息；内部细节保留在 ERROR 日志（middleware 记录完整 err）。
	if errors.Is(err, llmgatewaydomain.ErrUpstreamRequestFailed) {
		return PublicErrorDescriptor{Message: "上游模型服务请求失败，请稍后重试"}
	}
	if status >= 500 {
		return PublicErrorDescriptor{Message: "internal server error"}
	}
	return PublicErrorDescriptor{Message: err.Error()}
}

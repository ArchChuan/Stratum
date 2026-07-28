package middleware

import (
	"errors"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
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
	if status >= 500 {
		return PublicErrorDescriptor{Message: "internal server error"}
	}
	return PublicErrorDescriptor{Message: err.Error()}
}

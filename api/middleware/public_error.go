package middleware

import (
	"errors"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
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
	// MCP 连接/发现失败：错误链只含安全 sentinel，对外固定中文消息，不暴露
	// 上游地址与响应细节。发现阶段失败（如服务器未实现 resources/list）在
	// 客户端能力感知修复后不再出现，此分支覆盖真实不可达/协议不兼容场景。
	if errors.Is(err, mcpdomain.ErrTransportFailed) {
		return PublicErrorDescriptor{
			Message: "连接 MCP 服务器失败：服务器未响应或协议不兼容，请检查服务器地址与认证配置",
		}
	}
	if errors.Is(err, mcpdomain.ErrSessionMissing) {
		return PublicErrorDescriptor{Message: "MCP 连接已断开，请重新连接后再试"}
	}
	if msg, ok := approvalPublicMessage(err); ok {
		return PublicErrorDescriptor{Message: msg}
	}
	if status >= 500 {
		return PublicErrorDescriptor{Message: "internal server error"}
	}
	return PublicErrorDescriptor{Message: err.Error()}
}

// approvalPublicMessage 映射审批终态/操作 sentinel 为固定中文消息（D7/D8 工作台与
// 聊天页可解释文案）。仅 errors.Is 命中才返回 ok=true；未命中回退默认 err.Error()。
func approvalPublicMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, agentapp.ErrApprovalExpired):
		return "审批已过期", true
	case errors.Is(err, agentdomain.ErrApprovalPolicyChanged):
		return "权限策略已变更，请重新发起", true
	case errors.Is(err, agentdomain.ErrApprovalConversationGone):
		return "会话已删除，审批已失效", true
	case errors.Is(err, agentdomain.ErrApprovalSelfDecision):
		return "不能审批自己发起的请求", true
	case errors.Is(err, agentdomain.ErrApprovalRoleDenied):
		return "需要管理员权限", true
	case errors.Is(err, agentdomain.ErrApprovalAlreadyDecided):
		return "该审批已处理", true
	case errors.Is(err, agentdomain.ErrApprovalAlreadyExecuted):
		return "该工具已执行", true
	case errors.Is(err, agentdomain.ErrApprovalInvalidated):
		return "审批已失效", true
	}
	return "", false
}

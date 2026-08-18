package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// noAnswerEvidence 构造带无答案信号的 evidence 搜索结果。
func noAnswerEvidence(reason domain.NoAnswerReason) port.RAGSearchEvidence {
	return port.RAGSearchEvidence{
		Content: "",
		Sources: []port.RAGSearchSource{},
		NoAnswer: &domain.NoAnswerInfo{
			Reason:         reason,
			RetrievedCount: 4,
			FilteredCount:  3,
			BestScore:      0.31,
			Detail:         "detail",
		},
	}
}

// TestExecSearchKnowledgeTool_NoAnswer 验证空结果不再以空串进入模型：
// 拒答模板展开 reason，信号透传到执行状态，供 SSE done noAnswer 使用。
func TestExecSearchKnowledgeTool_NoAnswer(t *testing.T) {
	logger := zap.NewNop()
	toolStart := time.Now()

	t.Run("evidence empty result renders refusal template with reason", func(t *testing.T) {
		s := &ReActState{
			AgentKnowledgeWorkspaceIDs: []string{"kb1"},
			RAGSearchFnWithEvidence: func(_ context.Context, _ []string, _ string, _ int, _ string) (port.RAGSearchEvidence, error) {
				return noAnswerEvidence(domain.NoAnswerReason(constants.NoAnswerReasonThresholdFiltered)), nil
			},
			InternalToolResultGuardFn: untrustedGuardTest,
		}
		res := execSearchKnowledgeTool(context.Background(), toolCall("stratum_search_knowledge", "kb1"), s, toolStart, logger)
		require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
		// 模板展开 reason，空串永远不进入模型可见文本。
		want := strings.ReplaceAll(constants.AgentKnowledgeNoResultText, "%s", constants.NoAnswerReasonThresholdFiltered)
		require.Contains(t, res.content, want)
		require.NotContains(t, res.content, "\"\"")
		// 信号透传：reason 与统计保真。
		require.NotNil(t, s.NoAnswer)
		require.Equal(t, domain.NoAnswerReason(constants.NoAnswerReasonThresholdFiltered), s.NoAnswer.Reason)
		require.Equal(t, 4, s.NoAnswer.RetrievedCount)
		require.Equal(t, float32(0.31), s.NoAnswer.BestScore)
	})

	t.Run("plain empty result defaults to no_sources", func(t *testing.T) {
		s := &ReActState{
			AgentKnowledgeWorkspaceIDs: []string{"kb1"},
			RAGSearchFn: func(_ context.Context, _ []string, _ string, _ int, _ string) (string, error) {
				return "", nil
			},
			InternalToolResultGuardFn: untrustedGuardTest,
		}
		res := execSearchKnowledgeTool(context.Background(), toolCall("stratum_search_knowledge", "kb1"), s, toolStart, logger)
		require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
		want := strings.ReplaceAll(constants.AgentKnowledgeNoResultText, "%s", constants.NoAnswerReasonNoSources)
		require.Contains(t, res.content, want)
		// plain 路径无信号可及，状态不残留 stale 信号。
		require.Nil(t, s.NoAnswer)
	})

	t.Run("has answer clears stale noAnswer signal", func(t *testing.T) {
		// 前一次工具调用留下了信号：有答案的结果必须清除，禁止跨调用残留。
		s := &ReActState{
			AgentKnowledgeWorkspaceIDs: []string{"kb1"},
			RAGSearchFnWithEvidence: func(_ context.Context, _ []string, _ string, _ int, _ string) (port.RAGSearchEvidence, error) {
				return port.RAGSearchEvidence{Content: "answer content", Sources: []port.RAGSearchSource{
					{ChunkID: "c1", WorkspaceID: "kb1"},
				}}, nil
			},
			InternalToolResultGuardFn: untrustedGuardTest,
			NoAnswer: &domain.NoAnswerInfo{
				Reason: domain.NoAnswerReason(constants.NoAnswerReasonNoSources),
			},
		}
		res := execSearchKnowledgeTool(context.Background(), toolCall("stratum_search_knowledge", "kb1"), s, toolStart, logger)
		require.Equal(t, domain.ToolTraceStatusSuccess, res.status)
		require.Contains(t, res.content, "answer content")
		require.Nil(t, s.NoAnswer)
	})

	t.Run("guard failure suppresses noAnswer signal", func(t *testing.T) {
		// 执行失败是错误态而非"无答案"：信号不透传，避免 UI 把 guard 故障
		// 误显示为拒答；citation 证据元数据仍保留。
		s := &ReActState{
			AgentKnowledgeWorkspaceIDs: []string{"kb1"},
			RAGSearchFnWithEvidence: func(_ context.Context, _ []string, _ string, _ int, _ string) (port.RAGSearchEvidence, error) {
				return noAnswerEvidence(domain.NoAnswerReason(constants.NoAnswerReasonAccessRestricted)), nil
			},
			InternalToolResultGuardFn: failingGuard,
		}
		res := execSearchKnowledgeTool(context.Background(), toolCall("stratum_search_knowledge", "kb1"), s, toolStart, logger)
		require.Equal(t, domain.ToolTraceStatusError, res.status)
		require.Contains(t, res.content, "tool result validation failed")
		require.Nil(t, s.NoAnswer)
	})
}

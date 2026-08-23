package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const planCheckpointVersion = 1

// 增量快照版本:ReAct checkpoint 只存工具维度消息(role=tool / 携带 tool_calls),
// chat_messages 表已持久化 user/assistant 全量,恢复时把工具消息 merge 回
// chat_messages 重建的 base,避免全量快照恢复造成重复历史。
const messagesSnapshotVersion = 2

// toolMessagesSnapshotEnvelope 是 MessagesSnapshotJSON 的 v2 信封:版本号 +
// 工具维度消息集。旧二进制写裸 []port.LLMMessage 数组(v1,隐式版本 1)。
type toolMessagesSnapshotEnvelope struct {
	Version      int               `json:"version"`
	ToolMessages []port.LLMMessage `json:"tool_messages"`
}

// ExtractToolMessages 提取工具维度消息:role=tool 或携带 tool_calls 的消息。
// 纯文本 user/assistant 不入快照(chat_messages 已有);悬空 tool_calls 保留,
// 与既有全量快照语义一致(恢复后 LLM 上下文仍含未完成调用)。
func ExtractToolMessages(msgs []port.LLMMessage) []port.LLMMessage {
	out := make([]port.LLMMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			out = append(out, m)
		}
	}
	return out
}

// EncodeToolMessagesSnapshot 序列化工具维度增量快照(v2 信封)。空消息集编码为
// 空信封,恢复端视为无工具历史。
func EncodeToolMessagesSnapshot(msgs []port.LLMMessage) (json.RawMessage, error) {
	return json.Marshal(toolMessagesSnapshotEnvelope{
		Version:      messagesSnapshotVersion,
		ToolMessages: ExtractToolMessages(msgs),
	})
}

// MergeToolMessagesSnapshot 把 checkpoint 的工具消息快照合并进恢复 base。
// v2 信封:工具消息 append 到 base 末尾。base 由 chat_messages 历史 + 本轮 user
// 输入重建,只含 user/assistant,与工具维度交集为空,无需去重;顺序
// [system, history, user, assistant(tool_calls), tool, ...] 正确。
// v1/裸数组(旧二进制全量快照,含 user/assistant/工具):整体替换 base,防止新旧
// 混跑 append 造成重复历史。非法 JSON 返回 base(降级为无检查点恢复)。
func MergeToolMessagesSnapshot(raw json.RawMessage, base []port.LLMMessage) []port.LLMMessage {
	if len(raw) == 0 {
		return base
	}
	var envelope toolMessagesSnapshotEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Version == messagesSnapshotVersion {
		if len(envelope.ToolMessages) == 0 {
			return base
		}
		return append(base, envelope.ToolMessages...)
	}
	// v1/裸数组:整体替换。裸数组 JSON 解进 struct 必失败,走到这里;v2 信封对象
	// 解进 []LLMMessage 也失败,同样降级返回 base。空数组(`[]`)与 JSON `null`
	// 语义等同"无快照":ensureInitialCheckpoint 的 init checkpoint 其
	// MessagesSnapshotJSON 为 nil,经 PgCheckpointStore.Upsert 归一为 `[]`;
	// 若把空数组当 v1 全量快照整体替换,会清空组装好的 base(system/memory/user),
	// 首轮 LLM 请求 messages 变空(M1 回归)。真实 v1 全量快照恒非空。
	var saved []port.LLMMessage
	if json.Unmarshal(raw, &saved) == nil && len(saved) > 0 {
		return saved
	}
	return base
}

// PlanCheckpointWriter is the narrow persistence boundary used by ReAct plan actions.
type PlanCheckpointWriter interface {
	Upsert(ctx context.Context, tenantID string, cp domain.AgentExecutionCheckpoint) error
}

var ErrUnsupportedPlanCheckpoint = errors.New("unsupported plan checkpoint version")

type PlanCheckpointPayload struct {
	Plan                    *domain.Plan `json:"plan"`
	RemainingNodeBudget     int          `json:"remaining_node_budget"`
	RemainingRevisionBudget int64        `json:"remaining_revision_budget"`
	ActiveAttemptIDs        []string     `json:"active_attempt_ids,omitempty"`
	// 多 skill 叠加：全量激活数组优先；旧字段保留作单 skill 回退（新 payload 回填最后一条激活）。
	ActiveSkills          []CheckpointSkillRef `json:"active_skills,omitempty"`
	ActiveSkillID         string               `json:"active_skill_id,omitempty"`
	ActiveSkillRevisionID string               `json:"active_skill_revision_id,omitempty"`
}

// CheckpointSkillRef 是激活 skill 的持久化引用（不携带指令内容，恢复时从 catalog 解析）。
type CheckpointSkillRef struct {
	SkillID    string `json:"skill_id"`
	RevisionID string `json:"revision_id"`
}

type planCheckpointEnvelope struct {
	Version int `json:"version"`
	PlanCheckpointPayload
}

type PlanCheckpointIdentity struct {
	CheckpointID   string
	ExecutionID    string
	TraceID        string
	ConversationID string
	AgentID        string
	UserID         string
}

func EncodePlanCheckpoint(payload PlanCheckpointPayload) ([]byte, error) {
	return json.Marshal(planCheckpointEnvelope{Version: planCheckpointVersion, PlanCheckpointPayload: payload})
}

func DecodePlanCheckpoint(data []byte) (PlanCheckpointPayload, error) {
	var envelope planCheckpointEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return PlanCheckpointPayload{}, fmt.Errorf("decode plan checkpoint: %w", err)
	}
	if envelope.Version != planCheckpointVersion {
		return PlanCheckpointPayload{}, fmt.Errorf("%w: %d", ErrUnsupportedPlanCheckpoint, envelope.Version)
	}
	if envelope.Plan != nil && envelope.Plan.ID == "" {
		return PlanCheckpointPayload{}, errors.New("decode plan checkpoint: plan id is required when plan is present")
	}
	return envelope.PlanCheckpointPayload, nil
}

// PersistPlanCheckpointSnapshot carries optional ReAct-level state to snapshot
// alongside the plan checkpoint. All fields are optional; nil values are skipped.
type PersistPlanCheckpointSnapshot struct {
	Messages     json.RawMessage // serialized []port.LLMMessage
	AllToolCalls json.RawMessage // serialized []port.ToolCall
	Steps        int
}

func PersistPlanCheckpoint(
	ctx context.Context,
	writer PlanCheckpointWriter,
	tenantID string,
	identity PlanCheckpointIdentity,
	payload PlanCheckpointPayload,
	snapshot *PersistPlanCheckpointSnapshot,
) error {
	if writer == nil {
		return errors.New("plan checkpoint: writer is required")
	}
	runtimeState, err := EncodePlanCheckpoint(payload)
	if err != nil {
		return fmt.Errorf("plan checkpoint: encode: %w", err)
	}
	now := time.Now().UTC()
	checkpoint := domain.AgentExecutionCheckpoint{
		ID: identity.CheckpointID, ExecutionID: identity.ExecutionID, TraceID: identity.TraceID,
		ConversationID: identity.ConversationID, AgentID: identity.AgentID, UserID: identity.UserID,
		RuntimeStateJSON: runtimeState, Status: "running", CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(constants.PlanCheckpointTTL),
	}
	if snapshot != nil {
		checkpoint.StepIndex = snapshot.Steps
		if len(snapshot.Messages) > 0 {
			checkpoint.MessagesSnapshotJSON = snapshot.Messages
		}
		if len(snapshot.AllToolCalls) > 0 {
			checkpoint.CompletedToolCallsJSON = snapshot.AllToolCalls
		}
	}
	if err := writer.Upsert(ctx, tenantID, checkpoint); err != nil {
		return fmt.Errorf("plan checkpoint: persist: %w", err)
	}
	return nil
}

// checkpointSnapshot builds a PersistPlanCheckpointSnapshot from a ReActState.
// Returns nil when state is nil.
func checkpointSnapshot(state *ReActState) *PersistPlanCheckpointSnapshot {
	if state == nil {
		return nil
	}
	snapshot := &PersistPlanCheckpointSnapshot{Steps: state.Steps}
	if len(state.Messages) > 0 {
		if encoded, err := json.Marshal(state.Messages); err == nil {
			snapshot.Messages = json.RawMessage(encoded)
		}
	}
	if len(state.AllToolCalls) > 0 {
		if encoded, err := json.Marshal(state.AllToolCalls); err == nil {
			snapshot.AllToolCalls = json.RawMessage(encoded)
		}
	}
	return snapshot
}

// buildReActRuntimeState encodes ReAct-only runtime state (Actives) into
// the checkpoint's RuntimeStateJSON. Returns {} when there is nothing to persist.
func buildReActRuntimeState(state *ReActState) json.RawMessage {
	if state == nil || len(state.Actives) == 0 {
		return json.RawMessage("{}")
	}
	payload := PlanCheckpointPayload{ActiveSkills: make([]CheckpointSkillRef, 0, len(state.Actives))}
	for _, active := range state.Actives {
		payload.ActiveSkills = append(payload.ActiveSkills, CheckpointSkillRef{
			SkillID: active.SkillID, RevisionID: active.RevisionID,
		})
	}
	// 旧字段回填最后一条激活，供旧二进制降级恢复。
	last := state.Actives[len(state.Actives)-1]
	payload.ActiveSkillID = last.SkillID
	payload.ActiveSkillRevisionID = last.RevisionID
	if encoded, err := EncodePlanCheckpoint(payload); err == nil {
		return json.RawMessage(encoded)
	}
	return json.RawMessage("{}")
}

// PersistReActCheckpoint persists a lightweight ReAct execution checkpoint
// (no plan DAG). It snapshots Messages, tool calls, and the current graph node.
func PersistReActCheckpoint(
	ctx context.Context,
	writer PlanCheckpointWriter,
	tenantID string,
	identity PlanCheckpointIdentity,
	state *ReActState,
	currentNode string,
) error {
	if writer == nil || state == nil {
		return nil
	}
	snapshot := checkpointSnapshot(state)
	now := time.Now().UTC()
	cp := domain.AgentExecutionCheckpoint{
		ID:               identity.CheckpointID,
		ExecutionID:      identity.ExecutionID,
		TraceID:          identity.TraceID,
		ConversationID:   identity.ConversationID,
		AgentID:          identity.AgentID,
		UserID:           identity.UserID,
		CurrentNode:      currentNode,
		StepIndex:        state.Steps,
		RuntimeStateJSON: buildReActRuntimeState(state),
		Status:           "running",
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(constants.PlanCheckpointTTL),
	}
	if snapshot != nil {
		// 增量快照:ReAct 只持久化工具维度消息(v2 信封),恢复时 merge 回
		// chat_messages 重建的 base。禁止用 checkpointSnapshot 的全量 Messages
		// (含 user/assistant,全量恢复会与 chat_messages 重复历史)。
		if len(state.Messages) > 0 {
			if encoded, err := EncodeToolMessagesSnapshot(state.Messages); err == nil {
				snapshot.Messages = encoded
			}
		}
		if len(snapshot.Messages) > 0 {
			cp.MessagesSnapshotJSON = snapshot.Messages
		}
		if len(snapshot.AllToolCalls) > 0 {
			cp.CompletedToolCallsJSON = snapshot.AllToolCalls
		}
		cp.StepIndex = snapshot.Steps
	}
	if identity.CheckpointID == "" {
		cp.ID = fmt.Sprintf("%s-step-%d", identity.ExecutionID, state.Steps)
	}
	return writer.Upsert(ctx, tenantID, cp)
}

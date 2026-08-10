import { z } from 'zod';

// 与 internal/audit/domain/audit.go 的 AuditEvent 精确对齐：
// request_id/trace_id 非可选（无 omitempty），仅 before/after 可选。
export const auditActorSchema = z.object({
  actor_type: z.string(),
  actor_id: z.string(),
});

export const auditEventSchema = z.object({
  id: z.string(),
  tenant_id: z.string(),
  actor: auditActorSchema,
  action: z.string(),
  resource_type: z.string(),
  resource_id: z.string(),
  // before/after 后端类型是 json.RawMessage（omitempty），序列化后是对象/数组。
  before: z.unknown().optional(),
  after: z.unknown().optional(),
  request_id: z.string(),
  trace_id: z.string(),
  risk_level: z.string(),
  outcome: z.string(),
  occurred_at: z.string(),
});

export const auditEventsPageSchema = z.object({
  events: z.array(auditEventSchema),
  total: z.number().int().nonnegative(),
});

export type AuditEvent = z.infer<typeof auditEventSchema>;

// risk_level / outcome 是控制逻辑枚举，硬编码（后端无枚举端点）。
// Record 模式参照 MCPServersPage 的 STATUS_MAP。
export const RISK_LEVEL_LABELS: Record<string, string> = {
  low: '低',
  medium: '中',
  high: '高',
};

export const RISK_LEVEL_COLORS: Record<string, string> = {
  low: 'green',
  medium: 'orange',
  high: 'red',
};

export const OUTCOME_LABELS: Record<string, string> = {
  success: '成功',
  error: '失败',
};

export const OUTCOME_COLORS: Record<string, string> = {
  success: 'green',
  error: 'red',
};

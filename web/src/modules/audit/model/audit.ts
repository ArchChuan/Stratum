import { z } from 'zod';

// 与 proto/audit/audit.proto 对齐：字段 snake_case；before/after 为 JSONB
// 投影（空投影后端返回 null，由 .optional() 容错）。
export const resourceChangeAuditSchema = z.object({
  id: z.string(),
  resource_kind: z.string(),
  resource_id: z.string(),
  operation: z.string(),
  actor_id: z.string(),
  actor_name: z.string(),
  created_at: z.string(),
  before: z.unknown().optional(),
  after: z.unknown().optional(),
});

export const resourceChangeAuditsPageSchema = z.object({
  events: z.array(resourceChangeAuditSchema),
  total: z.number().int().nonnegative(),
});

export type ResourceChangeAudit = z.infer<typeof resourceChangeAuditSchema>;

// operation 是控制逻辑枚举，硬编码（后端无枚举端点）。
export const OPERATION_LABELS: Record<string, string> = {
  create: '创建',
  update: '更新',
  delete: '删除',
  publish: '发布',
  promote: '发布',
  rollback: '回滚',
  reject: '拒绝',
  pause: '暂停',
  activate: '激活',
};

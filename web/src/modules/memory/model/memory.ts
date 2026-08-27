import { z } from 'zod';

export const memoryEntrySchema = z
  .object({
    id: z.string().optional(),
    role: z.string().optional(),
    content: z.string().optional().default(''),
    tags: z.array(z.string()).optional().default([]),
    importance: z.number().optional(),
    timestamp: z.string().optional(),
  })
  .passthrough();
export type MemoryEntry = z.infer<typeof memoryEntrySchema>;

export const memorySearchResultSchema = z
  .object({
    entry: memoryEntrySchema.optional(),
    score: z.number().optional(),
  })
  .passthrough();
export type MemorySearchResult = z.infer<typeof memorySearchResultSchema>;

// 用户记忆事实（对齐后端 dto.MemoryFactResponse；注意时间字段为 created_at，
// 与 memoryEntrySchema 的 timestamp 不同，不复用旧 schema）。
export const memoryFactSchema = z.object({
  id: z.string(),
  scope: z.string(),
  content: z.string(),
  importance: z.number(),
  created_at: z.string(),
  updated_at: z.string(),
  confidence: z.number(),
  category: z.string(),
  source: z.string(),
  status: z.string(),
});
export type MemoryFact = z.infer<typeof memoryFactSchema>;

export const memoryListPageSchema = z.object({
  memories: z.array(memoryFactSchema),
  total: z.number(),
});
export type MemoryListPage = z.infer<typeof memoryListPageSchema>;

// 用户级记忆统计（对齐 dto.MemoryStatsResponse：memory_count 为当前用户 active
// facts 数，entity_count 为当前用户 active 实体数；与列表/实体列表同口径）。
export const memoryStatsSchema = z
  .object({
    memory_count: z.number().optional().default(0),
    entity_count: z.number().optional().default(0),
    // 租户是否已配置可用嵌入模型；false 时记忆页展示健康提示。
    embed_model_configured: z.boolean().optional().default(false),
  })
  .passthrough();
export type MemoryStats = z.infer<typeof memoryStatsSchema>;

// 用户记忆实体（轻量话题标签，对齐 dto.MemoryEntityResponse）。
export const memoryEntitySchema = z.object({
  id: z.string(),
  name: z.string(),
  entity_type: z.string(),
  fact_count: z.number(),
  last_seen_at: z.string(),
});
export type MemoryEntity = z.infer<typeof memoryEntitySchema>;

export const memoryEntityListPageSchema = z.object({
  entities: z.array(memoryEntitySchema),
  total: z.number(),
});
export type MemoryEntityListPage = z.infer<typeof memoryEntityListPageSchema>;

// 用户记忆事实管理列表页（对齐 dto.MemoryFactListResponse）。
export const memoryFactListPageSchema = z.object({
  facts: z.array(memoryFactSchema),
  total: z.number(),
});
export type MemoryFactListPage = z.infer<typeof memoryFactListPageSchema>;

// 更新事实响应：vector_sync_failed 为 true 表示内容已保存但向量同步失败待后台补偿。
export const updateMemoryFactResponseSchema = z.object({
  fact: memoryFactSchema,
  vector_sync_failed: z.boolean(),
});
export type UpdateMemoryFactResponse = z.infer<typeof updateMemoryFactResponseSchema>;

// 用户记忆摘要（对齐 dto.MemorySummaryResponse）。
export const memorySummarySchema = z.object({
  id: z.string(),
  summary: z.string(),
  tier: z.string(),
  importance: z.number(),
  conversation_id: z.string(),
  period_end: z.string(),
  created_at: z.string(),
});
export type MemorySummary = z.infer<typeof memorySummarySchema>;
export const memorySummaryListPageSchema = z.object({
  summaries: z.array(memorySummarySchema),
  total: z.number(),
});
export type MemorySummaryListPage = z.infer<typeof memorySummaryListPageSchema>;

// 用户记忆快照（对齐 dto.MemorySnapshotResponse）。
export const memorySnapshotSchema = z.object({
  agent_id: z.string(),
  work_context: z.array(z.string()),
  personal_context: z.array(z.string()),
  top_of_mind: z.array(z.string()),
  expires_at: z.string(),
  updated_at: z.string(),
  status: z.string(),
});
export type MemorySnapshot = z.infer<typeof memorySnapshotSchema>;
export const memorySnapshotListSchema = z.object({
  snapshots: z.array(memorySnapshotSchema),
});
export type MemorySnapshotList = z.infer<typeof memorySnapshotListSchema>;

// 记忆条目管理列表项（管理侧对齐 dto.MemoryEntryResponse；与 v1 memoryEntrySchema
// 的搜索/Embedding 形状不同，不复用旧 schema）。
export const memoryEntryItemSchema = z.object({
  id: z.string(),
  role: z.string(),
  content: z.string(),
  type: z.string(),
  scope: z.string(),
  importance: z.number(),
  created_at: z.string(),
  expires_at: z.string().nullable(),
});
export type MemoryEntryItem = z.infer<typeof memoryEntryItemSchema>;
export const memoryEntryListPageSchema = z.object({
  entries: z.array(memoryEntryItemSchema),
  total: z.number(),
});
export type MemoryEntryListPage = z.infer<typeof memoryEntryListPageSchema>;

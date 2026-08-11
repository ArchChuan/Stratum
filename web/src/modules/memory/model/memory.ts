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

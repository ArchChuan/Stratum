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

export const memoryStatsSchema = z
  .object({
    total_entries: z.number().optional().default(0),
    short_term_count: z.number().optional().default(0),
    long_term_count: z.number().optional().default(0),
    entity_count: z.number().optional().default(0),
    sessions_count: z.number().optional().default(0),
    active_users: z.number().optional().default(0),
    vector_count: z.number().optional().default(0),
    last_access_time: z.string().optional().default(''),
    storage_size_bytes: z.number().optional().default(0),
    // 租户是否已配置可用嵌入模型；false 时记忆页展示健康提示。
    embed_model_configured: z.boolean().optional().default(false),
  })
  .passthrough();
export type MemoryStats = z.infer<typeof memoryStatsSchema>;

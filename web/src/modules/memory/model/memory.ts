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
  })
  .passthrough();
export type MemoryStats = z.infer<typeof memoryStatsSchema>;

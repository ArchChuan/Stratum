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
    total_entities: z.number().optional().default(0),
    total_relations: z.number().optional().default(0),
    total_sessions: z.number().optional().default(0),
    vector_count: z.number().optional().default(0),
    enriched_count: z.number().optional().default(0),
  })
  .passthrough();
export type MemoryStats = z.infer<typeof memoryStatsSchema>;

export const memoryEntitySchema = z
  .object({
    name: z.string(),
    type: z.string().optional().default(''),
  })
  .passthrough();
export type MemoryEntity = z.infer<typeof memoryEntitySchema>;

export interface NewMemoryInput {
  role: string;
  content: string;
  tags: string[];
  importance: number;
  user_id?: string;
}

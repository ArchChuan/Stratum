import { z } from 'zod';

export const workspaceConfigSchema = z
  .object({
    embedding_model: z.string().optional().default(''),
    chunk_size: z.number().optional(),
    chunk_overlap: z.number().optional(),
    query_mode: z.string().optional(),
    top_k: z.number().optional(),
  })
  .passthrough();
export type WorkspaceConfig = z.infer<typeof workspaceConfigSchema>;

export const workspaceSchema = z
  .object({
    id: z.string().optional(),
    name: z.string(),
    description: z.string().optional().default(''),
    config: workspaceConfigSchema.optional(),
  })
  .passthrough();
export type Workspace = z.infer<typeof workspaceSchema>;

export const workspaceStatsSchema = z
  .object({
    description: z.string().optional().default(''),
    config: workspaceConfigSchema.optional(),
    stats: z
      .object({
        row_count: z.number().optional(),
        doc_count: z.number().optional(),
      })
      .passthrough()
      .optional(),
  })
  .passthrough();
export type WorkspaceStats = z.infer<typeof workspaceStatsSchema>;

export const querySourceSchema = z
  .object({
    document_id: z.string().optional().default(''),
    score: z.number().optional().default(0),
    content: z.string().optional().default(''),
  })
  .passthrough();
export type QuerySource = z.infer<typeof querySourceSchema>;

export const queryResultSchema = z
  .object({
    answer: z.string().optional().default(''),
    sources: z.array(querySourceSchema).optional().default([]),
  })
  .passthrough();
export type QueryResult = z.infer<typeof queryResultSchema>;

export interface CreateWorkspaceInput {
  name: string;
  description: string;
  config: {
    embedding_model: string;
    chunk_size: number;
    chunk_overlap: number;
    query_mode: string;
    top_k: number;
  };
}

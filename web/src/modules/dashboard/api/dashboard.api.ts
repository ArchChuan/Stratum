import { z } from 'zod';

import api from '@/services/client';

export const dashboardCountsSchema = z.object({
  agents: z.number().int().nonnegative(),
  skills: z.number().int().nonnegative(),
  knowledge_workspaces: z.number().int().nonnegative(),
  mcp_servers: z.number().int().nonnegative(),
  model_providers: z.number().int().nonnegative(),
  tenant_members: z.number().int().nonnegative(),
  workflows: z.number().int().nonnegative(),
  agent_user_messages_7d: z.number().int().nonnegative(),
});

// executionRow 字段由 contract test 冻结（agent_crud_handler.go executionRow），
// 前端不增删、不假设存在与否，全字段非可选。
export const dashboardExecutionSchema = z.object({
  id: z.string(),
  trace_id: z.string(),
  agent_id: z.string(),
  agent_name: z.string(),
  status: z.string(),
  input_preview: z.string(),
  output_preview: z.string(),
  error_message: z.string(),
  total_tokens: z.number().int().nonnegative(),
  duration_ms: z.number().int().nonnegative(),
  created_at: z.string(),
});

export const dashboardExecutionsPageSchema = z.object({
  executions: z.array(dashboardExecutionSchema),
  total: z.number().int().nonnegative(),
});

export const dashboardApi = {
  overview: async () => {
    const response = await api.get('/dashboard/overview');
    return dashboardCountsSchema.parse(response.data);
  },

  executions: async ({ page, pageSize }: { page: number; pageSize: number }) => {
    const response = await api.get('/agents/executions', { params: { page, page_size: pageSize } });
    return dashboardExecutionsPageSchema.parse(response.data);
  },
};

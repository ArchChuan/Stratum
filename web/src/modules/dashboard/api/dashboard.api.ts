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

export const dashboardApi = {
  overview: async () => {
    const response = await api.get('/dashboard/overview');
    return dashboardCountsSchema.parse(response.data);
  },
};

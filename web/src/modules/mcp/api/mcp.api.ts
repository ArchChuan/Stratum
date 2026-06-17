import { z } from 'zod';

import {
  mcpResourceSchema,
  mcpServerSchema,
  mcpToolSchema,
  type MCPResource,
  type MCPServer,
  type MCPServerConfig,
  type MCPTool,
} from '../model/mcp';

import api from '@/services/client';

export const mcpApi = {
  list: async (): Promise<MCPServer[]> => {
    const res = await api.get('/mcp/servers');
    return z.array(mcpServerSchema).parse(res.data?.servers ?? []);
  },
  connect: (cfg: MCPServerConfig) => api.post('/mcp/servers', cfg),
  disconnect: (id: string) => api.delete(`/mcp/servers/${id}`),
  tools: async (id: string): Promise<MCPTool[]> => {
    const res = await api.get(`/mcp/servers/${id}/tools`);
    return z.array(mcpToolSchema).parse(res.data?.tools ?? []);
  },
  resources: async (id: string): Promise<MCPResource[]> => {
    const res = await api.get(`/mcp/servers/${id}/resources`);
    return z.array(mcpResourceSchema).parse(res.data?.resources ?? []);
  },
};

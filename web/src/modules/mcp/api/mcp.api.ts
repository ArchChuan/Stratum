import { z } from 'zod';

import {
  mcpResourceSchema,
  mcpServerSchema,
  mcpToolSchema,
  type MCPResource,
  type MCPServer,
  type MCPServerConfig,
	type MCPTool,
	type MCPToolOption,
	type MCPToolPolicy,
	type MCPToolRiskLevel,
} from '../model/mcp';

import api from '@/services/client';

export const mcpApi = {
  list: async (): Promise<MCPServer[]> => {
    const res = await api.get('/mcp/servers');
    return z.array(mcpServerSchema).parse(res.data?.servers ?? []);
  },
  connect: (cfg: MCPServerConfig) => api.post('/mcp/servers', cfg),
  update: (id: string, cfg: MCPServerConfig) => api.put(`/mcp/servers/${id}`, cfg),
  getConfig: async (id: string): Promise<MCPServerConfig> => {
    const res = await api.get(`/mcp/servers/${id}/config`);
    return res.data as MCPServerConfig;
  },
  disconnect: (id: string) => api.delete(`/mcp/servers/${id}`),
  delete: (id: string) => api.delete(`/mcp/servers/${id}/config`),
  reconnect: (id: string) => api.post(`/mcp/servers/${id}/reconnect`),
  tools: async (id: string): Promise<MCPTool[]> => {
    const res = await api.get(`/mcp/servers/${id}/tools`);
    return z.array(mcpToolSchema).parse(res.data?.tools ?? []);
  },
	resources: async (id: string): Promise<MCPResource[]> => {
    const res = await api.get(`/mcp/servers/${id}/resources`);
    return z.array(mcpResourceSchema).parse(res.data?.resources ?? []);
	},
	toolOptions: async (): Promise<MCPToolOption[]> => {
		const servers = await mcpApi.list();
		const groups = await Promise.all(servers.map(async (server) => ({ server, tools: await mcpApi.tools(server.id).catch(() => []) })));
		return groups.flatMap(({ server, tools }) => tools.map((tool) => ({ id: `mcp:${server.id}:${tool.name}`, label: `${server.name || server.id} / ${tool.name}`, serverId: server.id, toolName: tool.name })));
	},
	toolPolicies: async (): Promise<MCPToolPolicy[]> => (await api.get('/mcp/tool-policies')).data?.policies ?? [],
	setToolPolicy: (serverId: string, toolName: string, riskLevel: MCPToolRiskLevel) => api.put(`/mcp/tool-policies/${encodeURIComponent(serverId)}/${encodeURIComponent(toolName)}`, { riskLevel }),
};

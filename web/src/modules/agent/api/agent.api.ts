import { z } from 'zod';

import {
  agentSchema,
  chatMessageSchema,
  conversationSchema,
  type Agent,
  type AgentFormValues,
  type ChatMessage,
  type Conversation,
  type ExecuteAgentPayload,
	type StreamCallbacks,
	type ToolApproval,
	type ToolApprovalResumeResult,
} from '../model/agent';

import { AGENT_EXEC_TIMEOUT_MS, DEFAULT_PAGE_SIZE } from '@/constants';
import api, { streamApiEvents } from '@/services/client';

export const agentApi = {
  list: async (): Promise<Agent[]> => {
    const res = await api.get('/agents');
    return z.array(agentSchema).parse(res.data?.agents ?? []);
  },
  get: async (id: string): Promise<Agent> => {
    const res = await api.get(`/agents/${id}`);
    return agentSchema.parse(res.data);
  },
  create: (data: AgentFormValues) => api.post('/agents', data),
  update: (id: string, data: AgentFormValues) => api.put(`/agents/${id}`, data),
  delete: (id: string) => api.delete(`/agents/${id}`),
  execute: (id: string, payload: ExecuteAgentPayload) =>
    api.post(`/agents/${id}/execute`, payload, { timeout: AGENT_EXEC_TIMEOUT_MS }),
	executions: (page = 1, pageSize = DEFAULT_PAGE_SIZE) =>
    api.get('/agents/executions', { params: { page, page_size: pageSize } }),
	models: async (): Promise<string[]> => {
    const res = await api.get('/models');
    return z.array(z.string()).parse(res.data?.models ?? []);
	},
	listToolApprovals: async (): Promise<ToolApproval[]> => {
		const res = await api.get('/agents/tool-approvals');
		return (res.data?.approvals ?? []).map((row: Record<string, unknown>) => ({
			approvalId: String(row.id || ''), agentId: String(row.agent_id || ''), toolName: String(row.tool_name || ''),
			serverId: String(row.server_id || ''), riskLevel: String(row.risk_level || ''), status: String(row.status || ''), expiresAt: String(row.expires_at || ''),
		}));
	},
	decideToolApproval: (id: string, decision: 'approved' | 'rejected', reason = '') => api.post(`/agents/tool-approvals/${id}/decision`, { decision, reason }),
	resumeToolApproval: async (id: string): Promise<ToolApprovalResumeResult> => {
		const res = await api.post(`/agents/tool-approvals/${id}/resume`);
		return res.data as ToolApprovalResumeResult;
	},
};

export const conversationApi = {
  list: async (agentId: string): Promise<Conversation[]> => {
    const res = await api.get(`/agents/${agentId}/conversations`);
    return z.array(conversationSchema).parse(res.data?.conversations ?? []);
  },
  create: async (agentId: string, name?: string): Promise<Conversation> => {
    const res = await api.post(`/agents/${agentId}/conversations`, name ? { name } : {});
    return conversationSchema.parse(res.data);
  },
  rename: (convId: string, name: string) => api.patch(`/conversations/${convId}`, { name }),
  delete: (convId: string) => api.delete(`/conversations/${convId}`),
  messages: async (convId: string): Promise<ChatMessage[]> => {
    const res = await api.get(`/conversations/${convId}/messages`);
    return z.array(chatMessageSchema).parse(res.data?.messages ?? []);
  },
  addMessage: (convId: string, role: string, content: string) =>
    api.post(`/conversations/${convId}/messages`, { role, content }),
};

export const executeAgentStream = (
  id: string,
  payload: ExecuteAgentPayload,
	{ onToken, onDone, onError, onApprovalRequired }: StreamCallbacks,
): AbortController => {
  let completed = false;

  return streamApiEvents(`/agents/${id}/execute/stream`, payload, {
    onEvent: (evt) => {
		const event = evt as { error?: string; done?: boolean; token?: unknown; status?: string; approvalId?: string; toolName?: string; serverId?: string; riskLevel?: string };
		if (event.status === 'waiting_approval' && event.approvalId) {
			completed = true;
			onApprovalRequired({ approvalId: event.approvalId, toolName: event.toolName || '', serverId: event.serverId || '', riskLevel: event.riskLevel || 'unclassified', status: event.status });
			return false;
		}
      if (event.error) {
        completed = true;
        onError(new Error(event.error));
        return false;
      }
      if (event.done) {
        completed = true;
        onDone(event);
        return false;
      }
      if (event.token != null) onToken(String(event.token));
      return true;
    },
    onClose: () => {
      if (!completed) onError(new Error('Stream closed unexpectedly'));
    },
    onError: (err) => {
      if (completed) return;
      onError(err);
    },
  });
};

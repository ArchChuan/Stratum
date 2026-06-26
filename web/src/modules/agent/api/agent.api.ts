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
} from '../model/agent';

import { AGENT_EXEC_TIMEOUT_MS, DEFAULT_PAGE_SIZE } from '@/constants';
import api, { getTokenRef } from '@/services/client';

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
  { onToken, onDone, onError }: StreamCallbacks,
): AbortController => {
  const ctrl = new AbortController();
  const tokenRef = getTokenRef();
  const token = (tokenRef as { current: string | null }).current;
  const base = (import.meta.env.VITE_API_BASE_URL as string) || '';

  fetch(`${base}/agents/${id}/execute/stream`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    credentials: 'include',
    body: JSON.stringify(payload),
    signal: ctrl.signal,
  })
    .then((res) => {
      if (!res.ok) {
        return res
          .json()
          .then((d: { message?: string; error?: string }) => {
            throw new Error(d.message || d.error || `HTTP ${res.status}`);
          });
      }
      const reader = res.body?.getReader();
      if (!reader) throw new Error('No readable stream');
      const decoder = new TextDecoder();
      let buf = '';
      let completed = false;

      const pump = (): Promise<void> =>
        reader.read().then(({ done, value }) => {
          if (done) {
            if (!completed) onError(new Error('Stream closed unexpectedly'));
            return;
          }
          buf += decoder.decode(value, { stream: true });
          const parts = buf.split('\n\n');
          buf = parts.pop() ?? '';
          for (const part of parts) {
            const line = part.trim();
            if (!line.startsWith('data:')) continue;
            const json = line.slice(5).trim();
            try {
              const evt = JSON.parse(json);
              if (evt.error) {
                completed = true;
                onError(new Error(evt.error));
                return;
              } else if (evt.done) {
                completed = true;
                onDone(evt);
                return;
              } else if (evt.token != null) {
                onToken(String(evt.token));
              }
            } catch {
              /* malformed chunk, skip */
            }
          }
          return pump();
        });

      return pump();
    })
    .catch((err: Error) => {
      if (err.name !== 'AbortError') onError(err);
    });

  return ctrl;
};

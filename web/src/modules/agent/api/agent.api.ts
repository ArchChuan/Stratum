import { z } from 'zod';

import {
  agentSchema,
  chatMessageSchema,
  conversationSchema,
  systemAssistantSettingsSchema,
  type Agent,
  type AgentFormValues,
  type ChatMessage,
  type Conversation,
  type ExecuteAgentPayload,
	type StreamCallbacks,
	type ToolApproval,
	type ToolApprovalResumeResult,
  type SystemAssistantSettings,
} from '../model/agent';

import {
  AGENT_EXEC_TIMEOUT_MS,
  AGENT_STREAM_RECONNECT_BASE_MS,
  AGENT_STREAM_RECONNECT_MAX_ATTEMPTS,
  AGENT_STREAM_RECONNECT_MAX_MS,
  DEFAULT_PAGE_SIZE,
} from '@/constants';
import api, { StreamRequestError, streamApiEvents } from '@/services/client';

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
  getSystemSettings: async (): Promise<SystemAssistantSettings> => {
    const res = await api.get('/agents/system/settings');
    return systemAssistantSettingsSchema.parse(res.data);
  },
  updateSystemSettings: async (data: { llmModel: string }): Promise<SystemAssistantSettings> => {
    const res = await api.put('/agents/system/settings', data);
    return systemAssistantSettingsSchema.parse(res.data);
  },
	listToolApprovals: async (): Promise<ToolApproval[]> => {
		const res = await api.get('/agents/tool-approvals');
		return (res.data?.approvals ?? []).map((row: Record<string, unknown>) => ({
			approvalId: String(row.id || ''), agentId: String(row.agent_id || ''), toolName: String(row.tool_name || ''),
			serverId: String(row.server_id || ''), riskLevel: String(row.risk_level || ''), status: String(row.status || ''), expiresAt: String(row.expires_at || ''),
			invalidationReason: row.invalidation_reason ? String(row.invalidation_reason) : undefined,
		}));
	},
	decideToolApproval: (id: string, decision: 'approved' | 'rejected', reason = '') => api.post(`/agents/tool-approvals/${id}/decision`, { decision, reason }),
	resumeToolApproval: async (id: string): Promise<ToolApprovalResumeResult> => {
		const res = await api.post(`/agents/tool-approvals/${id}/resume`);
		return res.data as ToolApprovalResumeResult;
	},
	pauseExecution: (agentId: string, executionId: string) =>
		api.post(`/agents/${agentId}/executions/${executionId}/pause`),
	resumeExecution: (agentId: string, executionId: string, payload: ExecuteAgentPayload) =>
		api.post(`/agents/${agentId}/executions/${executionId}/resume`, payload, { timeout: AGENT_EXEC_TIMEOUT_MS }),
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
	{ onToken, onDone, onError, onApprovalRequired, onExecutionId }: StreamCallbacks,
): AbortController => {
  // 自愈连接器:断点续接协议的服务端协作端。SSE 首帧(无条件、先于任何 token
  // 帧)下发 execution_id 作为恢复键,前端捕获后仅存内存;断线(网络/5xx)以指数
  // 退避携带同一 execution_id + 原 query/conversation_id 重发,服务端
  // resumeFromCheckpoint 从上次检查点续跑、只流新增 token,前端累积 = 完整答案
  // (无重复)。done/error 帧/approval/4xx/用户 cancel/超最大重试次数为终止条件。
  // 刷新降级为重新执行是正确语义(断线回合 user 消息未落库,重跑无重复历史),
  // 故 execution_id 不写入 sessionStorage。
  const outer = new AbortController();
  let executionId: string | undefined;
  let completed = false;
  let attempt = 0;
  let delay = AGENT_STREAM_RECONNECT_BASE_MS;
  let current: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  outer.signal.addEventListener('abort', () => {
    current?.abort();
    if (timer) clearTimeout(timer);
  });

  const isDisposed = () => completed || outer.signal.aborted;

  const scheduleReconnect = () => {
    if (isDisposed()) return;
    attempt += 1;
    if (attempt > AGENT_STREAM_RECONNECT_MAX_ATTEMPTS) {
      completed = true;
      onError(new Error(`Stream reconnected ${AGENT_STREAM_RECONNECT_MAX_ATTEMPTS} times without completion`));
      return;
    }
    const wait = delay;
    delay = Math.min(delay * 2, AGENT_STREAM_RECONNECT_MAX_MS);
    timer = setTimeout(connect, wait);
  };

  const connect = () => {
    if (isDisposed()) return;
    // 断线重发必须带已捕获的 execution_id:后端沿用同一执行供 resume 定位
    // checkpoint;首连不带(服务端生成并首帧下发)。
    current = streamApiEvents(
      `/agents/${id}/execute/stream`,
      executionId ? { ...payload, execution_id: executionId } : payload,
      {
        onEvent: (evt) => {
          const event = evt as { execution_id?: string; error?: string; code?: string; done?: boolean; token?: unknown; status?: string; approvalId?: string; toolName?: string; serverId?: string; riskLevel?: string };
          if (event.execution_id) {
            executionId = event.execution_id;
            onExecutionId?.(event.execution_id);
            return true; // 恢复键首帧,非终止,继续接收 token
          }
          if (event.status === 'waiting_approval' && event.approvalId) {
            completed = true;
            onApprovalRequired({ approvalId: event.approvalId, toolName: event.toolName || '', serverId: event.serverId || '', riskLevel: event.riskLevel || 'unclassified', status: event.status });
            return false;
          }
          if (event.error) {
            completed = true;
            onError(new StreamRequestError(event.error, undefined, event.code));
            return false;
          }
          if (event.done) {
            completed = true;
            onDone(event);
            return false;
          }
          if (event.token != null) {
            delay = AGENT_STREAM_RECONNECT_BASE_MS; // 收到数据流:退避重置
            onToken(String(event.token));
          }
          return true;
        },
        onClose: () => {
          if (!completed && !isDisposed()) scheduleReconnect();
        },
        onError: (err) => {
          if (isDisposed()) return;
          // 4xx 是客户端/协议错误,重发无意义,直接终止;网络断线与 5xx 退避重发。
          if (err instanceof StreamRequestError && err.status != null && err.status >= 400 && err.status < 500) {
            completed = true;
            onError(err);
            return;
          }
          scheduleReconnect();
        },
      },
    );
  };

  connect();
  return outer;
};

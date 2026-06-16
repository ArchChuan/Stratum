import api, { getTokenRef } from './client';
import { AGENT_EXEC_TIMEOUT_MS, DEFAULT_PAGE_SIZE } from '../constants';

export const getAllAgents = () => api.get('/agents');
export const getAgentById = (id) => api.get(`/agents/${id}`);
export const createAgent = (data) => api.post('/agents', data);
export const updateAgent = (id, data) => api.put(`/agents/${id}`, data);
export const executeAgent = (id, task) => api.post(`/agents/${id}/execute`, task, { timeout: AGENT_EXEC_TIMEOUT_MS });

export const executeAgentStream = (id, task, { onToken, onDone, onError }) => {
  const ctrl = new AbortController();
  const tokenRef = getTokenRef();
  const token = tokenRef.current;
  const base = import.meta.env.VITE_API_BASE_URL || '';

  fetch(`${base}/agents/${id}/execute/stream`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    credentials: 'include',
    body: JSON.stringify(task),
    signal: ctrl.signal,
  })
    .then((res) => {
      if (!res.ok) {
        return res.json().then((d) => { throw new Error(d.message || d.error || `HTTP ${res.status}`); });
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';

      const pump = () =>
        reader.read().then(({ done, value }) => {
          if (done) return;
          buf += decoder.decode(value, { stream: true });
          const parts = buf.split('\n\n');
          buf = parts.pop();
          parts.forEach((part) => {
            const line = part.trim();
            if (!line.startsWith('data:')) return;
            const json = line.slice(5).trim();
            try {
              const evt = JSON.parse(json);
              if (evt.error) {
                onError && onError(new Error(evt.error));
              } else if (evt.done) {
                onDone && onDone(evt);
              } else if (evt.token != null) {
                onToken && onToken(evt.token);
              }
            } catch (_) { /* malformed chunk, skip */ }
          });
          return pump();
        });

      return pump();
    })
    .catch((err) => {
      if (err.name !== 'AbortError') {
        onError && onError(err);
      }
    });

  return ctrl;
};

export const deleteAgent = (id) => api.delete(`/agents/${id}`);
export const getAgentExecutions = (page = 1, pageSize = DEFAULT_PAGE_SIZE) =>
  api.get('/agents/executions', { params: { page, page_size: pageSize } });
export const getAvailableModels = () => api.get('/models');

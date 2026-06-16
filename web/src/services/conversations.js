import api from './client';

export const listConversations = (agentId) => api.get(`/agents/${agentId}/conversations`);
export const createConversation = (agentId, name) => api.post(`/agents/${agentId}/conversations`, { name });
export const renameConversation = (convId, name) => api.patch(`/conversations/${convId}`, { name });
export const deleteConversation = (convId) => api.delete(`/conversations/${convId}`);
export const listMessages = (convId) => api.get(`/conversations/${convId}/messages`);
export const addMessage = (convId, role, content) =>
  api.post(`/conversations/${convId}/messages`, { role, content });

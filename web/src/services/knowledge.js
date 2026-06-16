import api from './client';

export const getKnowledgeWorkspaces = () => api.get('/knowledge/workspaces');
export const listWorkspaces = () => api.get('/knowledge/workspaces');
export const createWorkspace = (data) => api.post('/knowledge/workspaces', data);
export const getWorkspaceStats = (name) => api.get(`/knowledge/workspaces/${name}/stats`);
export const updateWorkspace = (name, data) => api.patch(`/knowledge/workspaces/${name}`, data);
export const deleteWorkspace = (name) => api.delete(`/knowledge/workspaces/${name}`);
export const ingestDocument = (formData) =>
  api.post('/knowledge/ingest', formData, { headers: { 'Content-Type': 'multipart/form-data' } });
export const queryKnowledge = (data) => api.post('/knowledge/query', data);

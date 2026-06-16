import api from './client';

export const getMCPServers = () => api.get('/api/v1/mcp/servers');
export const connectMCPServer = (data) => api.post('/api/v1/mcp/servers', data);
export const disconnectMCPServer = (id) => api.delete(`/api/v1/mcp/servers/${id}`);
export const getMCPServerTools = (id) => api.get(`/api/v1/mcp/servers/${id}/tools`);
export const getMCPServerResources = (id) => api.get(`/api/v1/mcp/servers/${id}/resources`);

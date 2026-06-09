import axios from 'axios';
import { message } from 'antd';

// Dev: Vite proxy forwards /auth /health /skills etc. to :8080, same-origin so cookies work.
// Prod: set VITE_API_BASE_URL to backend origin, or leave empty when co-hosted.
const api = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '',
  timeout: 10000,
  withCredentials: true,
});

let _tokenRef = { current: null };
let _reqInterceptor = null;
let _resInterceptor = null;

export const setupApiInterceptors = (tokenRef, onLogout) => {
  _tokenRef = tokenRef;

  if (_reqInterceptor !== null) {
    api.interceptors.request.eject(_reqInterceptor);
  }
  if (_resInterceptor !== null) {
    api.interceptors.response.eject(_resInterceptor);
  }

  _reqInterceptor = api.interceptors.request.use(
    (config) => {
      const hasAuth = config.headers.get
        ? config.headers.get('Authorization')
        : config.headers['Authorization'];
      if (!hasAuth) {
        const token = _tokenRef.current || localStorage.getItem('access_token');
        if (token) {
          config.headers['Authorization'] = `Bearer ${token}`;
        }
      }
      return config;
    },
    (error) => Promise.reject(error)
  );

  let isRefreshing = false;
  let pendingQueue = [];

  const processQueue = (error, token = null) => {
    pendingQueue.forEach((p) => (error ? p.reject(error) : p.resolve(token)));
    pendingQueue = [];
  };

  _resInterceptor = api.interceptors.response.use(
    (response) => response,
    async (error) => {
      const originalRequest = error.config;

      if (
        error.response?.status === 403 &&
        error.response?.data?.message
      ) {
        message.error(error.response.data.message);
      }

      if (
        error.response?.status === 401 &&
        !originalRequest._retry &&
        !originalRequest.url?.includes('/auth/refresh')
      ) {
        if (isRefreshing) {
          return new Promise((resolve, reject) => {
            pendingQueue.push({ resolve, reject });
          }).then(() => {
            originalRequest.headers.set
              ? originalRequest.headers.set('Authorization', null)
              : delete originalRequest.headers['Authorization'];
            return api(originalRequest);
          });
        }

        originalRequest._retry = true;
        isRefreshing = true;

        try {
          const res = await api.post('/auth/refresh');
          const newToken = res.data.access_token;
          _tokenRef.current = newToken;
          localStorage.setItem('access_token', newToken);
          processQueue(null, newToken);
          originalRequest.headers.set
            ? originalRequest.headers.set('Authorization', null)
            : delete originalRequest.headers['Authorization'];
          return api(originalRequest);
        } catch (refreshError) {
          processQueue(refreshError, null);
          localStorage.removeItem('access_token');
          onLogout?.();
          return Promise.reject(refreshError);
        } finally {
          isRefreshing = false;
        }
      }

      return Promise.reject(error);
    }
  );
};

// Health
export const checkHealth = () => api.get('/health');

// Auth
export const getMe = () => api.get('/auth/me');
export const postRegister = (data) => api.post('/auth/register', data);
export const postLogout = () => api.post('/auth/logout');
export const postRefresh = () => api.post('/auth/refresh');

// Tenant
export const createTenant = (data) => api.post('/admin/tenants', data);
export const joinTenant = (onboardingToken, inviteCode) =>
  api.post('/auth/register', { onboarding_token: onboardingToken, action: 'join', invitation_token: inviteCode });
export const getTenantMembers = () => api.get('/tenant/members');
export const inviteMember = (data) => api.post('/tenant/members/invite', data);
export const updateMemberRole = (userId, role) => api.patch(`/tenant/members/${userId}/role`, { role });
export const removeMember = (userId) => api.delete(`/tenant/members/${userId}`);
export const getTenantSettings = () => api.get('/tenant/settings');
export const updateTenant = (data) => api.patch('/tenant/settings', data);
export const getAllTenants = () => api.get('/admin/tenants');
export const setTenantEnabled = (tenantId, enabled) => api.patch(`/admin/tenants/${tenantId}`, { status: enabled ? 'active' : 'suspended' });
export const getTenantList = () => api.get('/tenant/list');
export const switchTenant = (tenantId) => api.post('/auth/switch-tenant', { tenant_id: tenantId });
export const createUserTenant = (name) => api.post('/auth/create-tenant', { tenant_name: name });

// Skills
export const getAllSkills = () => api.get('/skills');
export const getSkillById = (id) => api.get(`/skills/${id}`);
export const createSkill = (data) => api.post('/skills', data);
export const updateSkill = (id, data) => api.put(`/skills/${id}`, data);
export const deleteSkill = (id) => api.delete(`/skills/${id}`);

// Agents
export const getAllAgents = () => api.get('/agents');
export const getAgentById = (id) => api.get(`/agents/${id}`);
export const createAgent = (data) => api.post('/agents', data);
export const updateAgent = (id, data) => api.put(`/agents/${id}`, data);
export const executeAgent = (id, task) => api.post(`/agents/${id}/execute`, task);
export const deleteAgent = (id) => api.delete(`/agents/${id}`);
export const getAgentExecutions = () => api.get('/agents/executions');
export const getAvailableModels = () => api.get('/models');

// Knowledge
export const getKnowledgeWorkspaces = () => api.get('/knowledge/workspaces');

// Memory
export const createSession = (data) => api.post('/memory/sessions', data);
export const addMemory = (data) => api.post('/memory', data);
export const getMemoryById = (id) => api.get(`/memory/${id}`);
export const searchMemory = (data) => api.post('/memory/search', data);
export const deleteMemory = (id) => api.delete(`/memory/${id}`);
export const getMemoryStats = (params) => api.get('/memory/stats', { params });
export const clearSession = (sessionId, params) => api.delete(`/memory/session/${sessionId}`, { params });
export const getMemoryEntities = (params) => api.get('/memory/entities', { params });
export const extractEntities = (data) => api.post('/memory/extract-entities', data);
export const getMemorySummary = (sessionId, params) => api.get(`/memory/summary/${sessionId}`, { params });

// MCP
export const getMCPServers = () => api.get('/api/v1/mcp/servers');
export const connectMCPServer = (data) => api.post('/api/v1/mcp/servers', data);
export const disconnectMCPServer = (id) => api.delete(`/api/v1/mcp/servers/${id}`);
export const getMCPServerTools = (id) => api.get(`/api/v1/mcp/servers/${id}/tools`);
export const getMCPServerResources = (id) => api.get(`/api/v1/mcp/servers/${id}/resources`);

// Knowledge Workspaces
export const listWorkspaces = () => api.get('/knowledge/workspaces');
export const createWorkspace = (data) => api.post('/knowledge/workspaces', data);
export const getWorkspaceStats = (name) => api.get(`/knowledge/workspaces/${name}/stats`);
export const updateWorkspace = (name, data) => api.patch(`/knowledge/workspaces/${name}`, data);
export const deleteWorkspace = (name) => api.delete(`/knowledge/workspaces/${name}`);
export const ingestDocument = (formData) =>
  api.post('/knowledge/ingest', formData, { headers: { 'Content-Type': 'multipart/form-data' } });
export const queryKnowledge = (data) => api.post('/knowledge/query', data);

export default api;

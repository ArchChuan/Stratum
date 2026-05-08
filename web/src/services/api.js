import axios from 'axios';

// 创建 axios 实例
const api = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080',
  timeout: 10000,
});

// 请求拦截器
api.interceptors.request.use(
  (config) => {
    // 可以在这里添加认证 token
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

// 响应拦截器
api.interceptors.response.use(
  (response) => {
    return response;
  },
  (error) => {
    console.error('API Error:', error);
    return Promise.reject(error);
  }
);

// Health Check
export const checkHealth = () => api.get('/health');

// Skills API
export const getAllSkills = () => api.get('/skills');
export const getSkillById = (id) => api.get(`/skills/${id}`);
export const createSkill = (data) => api.post('/skills', data);
// 移除了 executeSkill 函数，因为技能只能通过代理执行

// Agents API
export const getAllAgents = () => api.get('/agents');
export const getAgentById = (id) => api.get(`/agents/${id}`);
export const createAgent = (data) => api.post('/agents', data);
export const executeAgent = (id, task) => api.post(`/agents/${id}/execute`, task);

export default api;
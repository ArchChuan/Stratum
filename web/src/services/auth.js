import api from './client';

export const checkHealth = () => api.get('/health');
export const getMe = () => api.get('/auth/me');
export const postRegister = (data) => api.post('/auth/register', data);
export const postLogout = () => api.post('/auth/logout');
export const postRefresh = () => api.post('/auth/refresh');

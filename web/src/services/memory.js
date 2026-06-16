import api from './client';

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

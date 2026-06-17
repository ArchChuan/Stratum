import { z } from 'zod';

import {
  memoryEntitySchema,
  memorySearchResultSchema,
  memoryStatsSchema,
  type MemoryEntity,
  type MemorySearchResult,
  type MemoryStats,
  type NewMemoryInput,
} from '../model/memory';

import api from '@/services/client';

export const memoryApi = {
  search: async (data: { query: string; limit?: number }): Promise<MemorySearchResult[]> => {
    const res = await api.post('/memory/search', data);
    return z.array(memorySearchResultSchema).parse(res.data?.results ?? []);
  },
  add: (data: NewMemoryInput) => api.post('/memory', data),
  delete: (id: string) => api.delete(`/memory/${id}`),
  stats: async (params?: Record<string, unknown>): Promise<MemoryStats> => {
    const res = await api.get('/memory/stats', { params });
    return memoryStatsSchema.parse(res.data ?? {});
  },
  entities: async (params: { tenant_id?: string; user_id?: string }): Promise<MemoryEntity[]> => {
    const res = await api.get('/memory/entities', { params });
    return z.array(memoryEntitySchema).parse(res.data?.entities ?? []);
  },
  summary: async (
    sessionId: string,
    params: { tenant_id?: string; user_id?: string },
  ): Promise<string> => {
    const res = await api.get(`/memory/summary/${sessionId}`, { params });
    return String(res.data?.summary ?? '');
  },
  createSession: (data: Record<string, unknown>) => api.post('/memory/sessions', data),
  clearSession: (sessionId: string, params?: Record<string, unknown>) =>
    api.delete(`/memory/session/${sessionId}`, { params }),
  extractEntities: (data: Record<string, unknown>) => api.post('/memory/extract-entities', data),
};

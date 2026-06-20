import { z } from 'zod';

import {
  memorySearchResultSchema,
  memoryStatsSchema,
  type MemorySearchResult,
  type MemoryStats,
} from '../model/memory';

import api from '@/services/client';

export const memoryApi = {
  search: async (data: { query: string; limit?: number }): Promise<MemorySearchResult[]> => {
    const res = await api.post('/memory/search', data);
    return z.array(memorySearchResultSchema).parse(res.data?.results ?? []);
  },
  delete: (id: string) => api.delete(`/memory/${id}`),
  stats: async (params?: Record<string, unknown>): Promise<MemoryStats> => {
    const res = await api.get('/memory/stats', { params });
    return memoryStatsSchema.parse(res.data ?? {});
  },
  summary: async (
    sessionId: string,
    params: { tenant_id?: string; user_id?: string },
  ): Promise<string> => {
    const res = await api.get(`/memory/summary/${sessionId}`, { params });
    return String(res.data?.summary ?? '');
  },
};

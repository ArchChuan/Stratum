import { memoryListPageSchema, memoryStatsSchema, type MemoryFact, type MemoryListPage, type MemoryStats } from '../model/memory';

import api from '@/services/client';

export interface MyMemoriesFilter {
  page: number;
  pageSize: number;
}

export const memoryUserApi = {
  listMyMemories: async (filter: MyMemoriesFilter): Promise<MemoryListPage> => {
    const response = await api.get('/memory', {
      params: { page: filter.page, page_size: filter.pageSize },
    });
    return memoryListPageSchema.parse(response.data);
  },

  getStats: async (): Promise<MemoryStats> => {
    const response = await api.get('/memory/stats');
    return memoryStatsSchema.parse(response.data);
  },

  deleteMemory: async (id: string): Promise<void> => {
    await api.delete(`/memory/${encodeURIComponent(id)}`);
  },

  clearMyMemories: async (): Promise<void> => {
    await api.delete('/memory/clear');
  },
};

export type { MemoryFact };

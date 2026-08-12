import {
  memoryEntityListPageSchema,
  memoryListPageSchema,
  memoryStatsSchema,
  type MemoryEntity,
  type MemoryEntityListPage,
  type MemoryFact,
  type MemoryListPage,
  type MemoryStats,
} from '../model/memory';

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

  listMyEntities: async (filter: MyMemoriesFilter): Promise<MemoryEntityListPage> => {
    const response = await api.get('/memory/entities', {
      params: { page: filter.page, page_size: filter.pageSize },
    });
    return memoryEntityListPageSchema.parse(response.data);
  },

  clearMyMemories: async (): Promise<void> => {
    await api.delete('/memory/clear');
  },
};

export type { MemoryEntity, MemoryFact };

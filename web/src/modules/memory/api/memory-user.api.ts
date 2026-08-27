import {
  memoryEntityListPageSchema,
  memoryEntryListPageSchema,
  memoryFactListPageSchema,
  memoryFactSchema,
  memoryListPageSchema,
  memorySnapshotListSchema,
  memorySnapshotSchema,
  memoryStatsSchema,
  memorySummaryListPageSchema,
  updateMemoryFactResponseSchema,
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

export interface MemoryListParams {
  page: number;
  page_size: number;
  q?: string;
  importance_min?: number;
  importance_max?: number;
  category?: string;
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

  listFacts(params: MemoryListParams) {
    return api.get('/memory/facts', { params }).then((res) => memoryFactListPageSchema.parse(res.data));
  },
  getFact(id: string) {
    return api.get(`/memory/facts/${id}`).then((res) => memoryFactSchema.parse(res.data));
  },
  updateFact(id: string, data: { content?: string; importance?: number; category?: string }) {
    return api.patch(`/memory/facts/${id}`, data).then((res) => updateMemoryFactResponseSchema.parse(res.data));
  },
  deleteFact(id: string) {
    return api.delete(`/memory/facts/${id}`);
  },
  deleteEntity(id: string) {
    return api.delete(`/memory/entities/${id}`);
  },
  listSummaries(params: { page: number; page_size: number }) {
    return api.get('/memory/summaries', { params }).then((res) => memorySummaryListPageSchema.parse(res.data));
  },
  deleteSummary(id: string) {
    return api.delete(`/memory/summaries/${id}`);
  },
  listSnapshots() {
    return api.get('/memory/snapshots').then((res) => memorySnapshotListSchema.parse(res.data));
  },
  updateSnapshot(agentId: string, data: { work_context: string[]; personal_context: string[]; top_of_mind: string[] }) {
    return api.patch(`/memory/snapshots/${agentId}`, data).then((res) => memorySnapshotSchema.parse(res.data));
  },
  deleteSnapshot(agentId: string) {
    return api.delete(`/memory/snapshots/${agentId}`);
  },
  listEntries(params: { page: number; page_size: number; q?: string }) {
    return api.get('/memory/entries', { params }).then((res) => memoryEntryListPageSchema.parse(res.data));
  },
  deleteEntry(id: string) {
    return api.delete(`/memory/entries/${id}`);
  },
};

export type { MemoryEntity, MemoryFact };

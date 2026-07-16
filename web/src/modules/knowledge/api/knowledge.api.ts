import { z } from 'zod';

import {
  documentSchema,
  queryResultSchema,
  workspaceSchema,
  workspaceStatsSchema,
  type CreateWorkspaceInput,
  type KnowledgeDocument,
  type QueryResult,
  type Workspace,
  type WorkspaceStats,
} from '../model/knowledge';

import api from '@/services/client';

interface QueryInput {
  question: string;
  workspace: string;
  mode?: string;
  topK?: number;
}

export const knowledgeApi = {
  list: async (): Promise<Workspace[]> => {
    const res = await api.get('/knowledge/workspaces');
    return z.array(workspaceSchema).parse(res.data?.workspaces ?? []);
  },
  create: (data: CreateWorkspaceInput) => api.post('/knowledge/workspaces', data),
  stats: async (name: string): Promise<WorkspaceStats> => {
    const res = await api.get(`/knowledge/workspaces/${name}/stats`);
    return workspaceStatsSchema.parse(res.data ?? {});
  },
  update: (name: string, data: Record<string, unknown>) =>
    api.patch(`/knowledge/workspaces/${name}`, data),
  delete: (name: string) => api.delete(`/knowledge/workspaces/${name}`),
  ingest: (formData: FormData) =>
    api.post('/knowledge/ingest', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    }),
  listDocuments: async (name: string): Promise<KnowledgeDocument[]> => {
    const res = await api.get(`/knowledge/workspaces/${name}/documents`);
    return z.array(documentSchema).parse(res.data?.documents ?? []);
  },
  deleteDocument: (name: string, documentID: string) =>
    api.delete(`/knowledge/workspaces/${encodeURIComponent(name)}/documents/${encodeURIComponent(documentID)}`),
  query: async (data: QueryInput): Promise<QueryResult> => {
    const res = await api.post('/knowledge/query', data);
    return queryResultSchema.parse(res.data ?? {});
  },
};

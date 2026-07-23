import { z } from 'zod';

import {
  workflowDefinitionSchema,
  workflowPageSchema,
  workflowVersionPageSchema,
  workflowVersionSchema,
  type WorkflowDraftPayload,
} from '../model/workflow';

import api from '@/services/client';

const validResponseSchema = z.object({ valid: z.literal(true) });

export const workflowApi = {
  listWorkflows: async ({ query = '', page, pageSize }: { query?: string; page: number; pageSize: number }) => {
    const response = await api.get('/workflows', { params: { query, page, page_size: pageSize } });
    return workflowPageSchema.parse(response.data);
  },
  getWorkflow: async (workflowId: string) => {
    const response = await api.get(`/workflows/${workflowId}`);
    return workflowDefinitionSchema.parse(response.data);
  },
  createWorkflow: async (payload: WorkflowDraftPayload) => {
    const response = await api.post('/workflows', payload);
    return workflowDefinitionSchema.parse(response.data);
  },
  updateWorkflowDraft: async (workflowId: string, payload: WorkflowDraftPayload & { expected_revision: number }) => {
    const response = await api.put(`/workflows/${workflowId}/draft`, payload);
    return workflowDefinitionSchema.parse(response.data);
  },
  validateWorkflow: async (workflowId: string) => {
    const response = await api.post(`/workflows/${workflowId}/validate`);
    return validResponseSchema.parse(response.data);
  },
  publishWorkflow: async (workflowId: string) => {
    const response = await api.post(`/workflows/${workflowId}/publish`);
    return workflowVersionSchema.parse(response.data);
  },
  listWorkflowVersions: async (workflowId: string, { page, pageSize }: { page: number; pageSize: number }) => {
    const response = await api.get(`/workflows/${workflowId}/versions`, { params: { page, page_size: pageSize } });
    return workflowVersionPageSchema.parse(response.data);
  },
  getWorkflowVersion: async (workflowId: string, versionId: string) => {
    const response = await api.get(`/workflows/${workflowId}/versions/${versionId}`);
    return workflowVersionSchema.parse(response.data);
  },
};

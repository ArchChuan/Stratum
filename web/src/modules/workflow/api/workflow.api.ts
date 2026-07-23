import { z } from 'zod';

import {
  workflowDefinitionSchema,
  workflowRunDetailSchema,
  workflowRunPageSchema,
  workflowRunSchema,
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
  startWorkflowRun: async (payload: { version_id: string; task: string; fields: Record<string, unknown>; idempotency_key: string }) => {
    const response = await api.post('/workflow-runs', payload);
    return z.object({ run_id: z.string(), status: z.string() }).parse(response.data);
  },
  listWorkflowRuns: async ({ definitionId = '', status = '', page, pageSize }: { definitionId?: string; status?: string; page: number; pageSize: number }) => {
    const response = await api.get('/workflow-runs', { params: { definition_id: definitionId, status, page, page_size: pageSize } });
    return workflowRunPageSchema.parse(response.data);
  },
  getWorkflowRun: async (runId: string) => {
    const response = await api.get(`/workflow-runs/${runId}`);
    return workflowRunDetailSchema.parse(response.data);
  },
  cancelWorkflowRun: async (runId: string, payload: { expected_generation: number; reason?: string }) => {
    const response = await api.post(`/workflow-runs/${runId}/cancel`, payload);
    return workflowRunSchema.parse(response.data);
  },
  pauseWorkflowRun: async (runId: string, payload: { expected_generation: number; reason: string }) => {
    const response = await api.post(`/workflow-runs/${runId}/pause`, payload);
    return workflowRunSchema.parse(response.data);
  },
  resumeWorkflowRun: async (runId: string, payload: { expected_generation: number }) => {
    const response = await api.post(`/workflow-runs/${runId}/resume`, payload);
    return workflowRunSchema.parse(response.data);
  },
  decideWorkflowApproval: async (approvalId: string, payload: { run_id: string; attempt_id: string; expected_generation: number; decision: 'approve' | 'reject'; comment?: string }) => {
    const response = await api.post(`/workflow-approvals/${approvalId}/decision`, payload);
    return z.object({ status: z.literal('decided') }).parse(response.data);
  },
  resolveWorkflowManualIntervention: async (runId: string, effectId: string, payload: { expected_generation: number; action: 'mark_succeeded' | 'retry' | 'terminate'; output_summary?: string }) => {
    const response = await api.post(`/workflow-runs/${runId}/manual-interventions/${effectId}/resolve`, payload);
    return z.object({ status: z.literal('resolved') }).parse(response.data);
  },
};

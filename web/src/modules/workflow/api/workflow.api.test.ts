import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from './workflow.api';

import api from '@/services/client';

vi.mock('@/services/client', () => ({
  default: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
}));

const definition = {
  id: 'workflow-1',
  name: '客户研究',
  description: '',
  revision: 1,
  spec: { nodes: [{ id: 'approval', type: 'approval' }], edges: [] },
  input_schema: { task_label: '任务', fields: [] },
  created_at: '2026-07-23T00:00:00Z',
  updated_at: '2026-07-23T00:00:00Z',
};

describe('workflowApi', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('uses the shared client for catalog and definition reads', async () => {
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: { workflows: [], total: 0, page: 1, page_size: 20 } })
      .mockResolvedValueOnce({ data: definition });

    await expect(workflowApi.listWorkflows({ query: '研究', page: 1, pageSize: 20 })).resolves.toMatchObject({ total: 0 });
    await expect(workflowApi.getWorkflow('workflow-1')).resolves.toMatchObject({ id: 'workflow-1' });
    expect(api.get).toHaveBeenNthCalledWith(1, '/workflows', { params: { query: '研究', page: 1, page_size: 20 } });
    expect(api.get).toHaveBeenNthCalledWith(2, '/workflows/workflow-1');
  });

  it('creates, updates, validates, publishes, and lists immutable versions', async () => {
    vi.mocked(api.post)
      .mockResolvedValueOnce({ data: definition })
      .mockResolvedValueOnce({ data: { valid: true } })
      .mockResolvedValueOnce({ data: { ...definition, definition_id: definition.id, version: 1 } });
    vi.mocked(api.put).mockResolvedValueOnce({ data: { ...definition, revision: 2 } });
    vi.mocked(api.get).mockResolvedValueOnce({ data: { versions: [], total: 0, page: 1, page_size: 20 } });

    const draft = { name: definition.name, description: '', spec: definition.spec, input_schema: definition.input_schema };
    await workflowApi.createWorkflow(draft);
    await workflowApi.updateWorkflowDraft('workflow-1', { ...draft, expected_revision: 1 });
    await expect(workflowApi.validateWorkflow('workflow-1')).resolves.toEqual({ valid: true });
    await workflowApi.publishWorkflow('workflow-1');
    await workflowApi.listWorkflowVersions('workflow-1', { page: 1, pageSize: 20 });

    expect(api.post).toHaveBeenNthCalledWith(1, '/workflows', draft);
    expect(api.put).toHaveBeenCalledWith('/workflows/workflow-1/draft', { ...draft, expected_revision: 1 });
    expect(api.post).toHaveBeenNthCalledWith(2, '/workflows/workflow-1/validate');
    expect(api.post).toHaveBeenNthCalledWith(3, '/workflows/workflow-1/publish');
    expect(api.get).toHaveBeenCalledWith('/workflows/workflow-1/versions', { params: { page: 1, page_size: 20 } });
  });

  it('starts, lists, reads, and controls workflow runs through the shared client', async () => {
    const run = { id: 'run-1', definition_id: 'workflow-1', version_id: 'version-1', version: 1, status: 'running', snapshot: { nodes: [], edges: [] }, input: { task: '研究' }, output: '', generation: 1, created_by: 'user-1', created_at: '2026-07-23T00:00:00Z', updated_at: '2026-07-23T00:00:00Z' };
    vi.mocked(api.post)
      .mockResolvedValueOnce({ data: { run_id: 'run-1', status: 'queued' } })
      .mockResolvedValueOnce({ data: run });
    vi.mocked(api.get)
      .mockResolvedValueOnce({ data: { runs: [run], total: 1, page: 1, page_size: 20 } })
      .mockResolvedValueOnce({ data: { run, node_attempts: [], approvals: [], effect_intents: [], progress: { completed: 0, total: 0 }, available_actions: ['cancel'] } });

    const payload = { version_id: 'version-1', task: '研究', fields: {}, idempotency_key: 'idem-1' };
    await workflowApi.startWorkflowRun(payload);
    await workflowApi.listWorkflowRuns({ status: 'running', page: 1, pageSize: 20 });
    await workflowApi.getWorkflowRun('run-1');
    await workflowApi.cancelWorkflowRun('run-1', { expected_generation: 1, reason: '停止' });
    expect(api.post).toHaveBeenNthCalledWith(1, '/workflow-runs', payload);
    expect(api.get).toHaveBeenNthCalledWith(1, '/workflow-runs', { params: { definition_id: '', status: 'running', page: 1, page_size: 20 } });
    expect(api.post).toHaveBeenNthCalledWith(2, '/workflow-runs/run-1/cancel', { expected_generation: 1, reason: '停止' });
  });
});

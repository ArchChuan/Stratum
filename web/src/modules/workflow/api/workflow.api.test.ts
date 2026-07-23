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
});

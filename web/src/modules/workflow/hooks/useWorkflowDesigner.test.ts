import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { useWorkflowDesigner } from './useWorkflowDesigner';

vi.mock('antd', () => ({ message: { success: vi.fn(), error: vi.fn() } }));
vi.mock('../api/workflow.api', () => ({ workflowApi: {
  getWorkflow: vi.fn(),
  createWorkflow: vi.fn(),
  updateWorkflowDraft: vi.fn(),
  validateWorkflow: vi.fn(),
  publishWorkflow: vi.fn(),
} }));

const definition = {
  id: 'workflow-1', name: '研究流程', description: '', revision: 3,
  spec: { nodes: [{ id: 'node-1', type: 'approval' as const, name: '确认', agent_id: '', input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0 }], edges: [], max_concurrency: 0 },
  input_schema: { task_label: '主题', task_description: '', fields: [] },
  created_at: '2026-07-23T00:00:00Z', updated_at: '2026-07-23T00:00:00Z',
};

describe('useWorkflowDesigner', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(workflowApi.getWorkflow).mockResolvedValue(definition);
  });

  it('saves with the current revision and validates only the saved revision', async () => {
    vi.mocked(workflowApi.updateWorkflowDraft).mockResolvedValue({ ...definition, revision: 4 });
    vi.mocked(workflowApi.validateWorkflow).mockResolvedValue({ valid: true });
    const { result } = renderHook(() => useWorkflowDesigner('workflow-1'));
    await waitFor(() => expect(result.current.definition?.revision).toBe(3));
    act(() => result.current.dispatch({ type: 'node.rename', nodeId: 'node-1', name: '主管确认' }));
    expect(result.current.dirty).toBe(true);
    await act(async () => { await result.current.save(); });
    expect(workflowApi.updateWorkflowDraft).toHaveBeenCalledWith('workflow-1', expect.objectContaining({ expected_revision: 3 }));
    expect(result.current.dirty).toBe(false);
    await act(async () => { await result.current.validate(); });
    expect(result.current.validatedRevision).toBe(4);
  });

  it('preserves local graph state when the server reports a revision conflict', async () => {
    vi.mocked(workflowApi.updateWorkflowDraft).mockRejectedValue({ response: { status: 409, data: { error: 'revision conflict' } } });
    const { result } = renderHook(() => useWorkflowDesigner('workflow-1'));
    await waitFor(() => expect(result.current.definition).not.toBeNull());
    act(() => result.current.dispatch({ type: 'node.rename', nodeId: 'node-1', name: '本地修改' }));
    await act(async () => { await result.current.save(); });
    expect(result.current.editor.spec.nodes[0].name).toBe('本地修改');
    expect(result.current.dirty).toBe(true);
  });
});

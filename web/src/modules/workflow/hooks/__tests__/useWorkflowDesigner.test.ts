import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../../api/workflow.api';
import type { WorkflowDefinition } from '../../model/workflow';
import { useWorkflowDesigner } from '../useWorkflowDesigner';

vi.mock('../../api/workflow.api', () => ({
  workflowApi: {
    getWorkflow: vi.fn(),
    createWorkflow: vi.fn(),
    updateWorkflowDraft: vi.fn(),
    validateWorkflow: vi.fn(),
    publishWorkflow: vi.fn(),
  },
}));
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'u-1' } }),
  useTenantRole: () => ({ role: 'member', isAdmin: false, isOwner: false, isMember: true, hasTenantRole: () => false }),
}));
// 共享申请 hook（useRequestEditorAccess）转调 operationProposalApi，mock 其 API 依赖。
vi.mock('@/modules/operation-gate', () => ({
  operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({}) },
}));

const definition = (editors: string[]): WorkflowDefinition => ({
  id: 'wf-1',
  name: 'Research',
  description: '',
  revision: 1,
  spec: { nodes: [], edges: [], max_concurrency: 0 },
  input_schema: { task_label: '任务', task_description: '', fields: [] },
  created_by: 'u-2',
  editors,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
});

describe('useWorkflowDesigner canEdit', () => {
  beforeEach(() => vi.clearAllMocks());

  it('白名单成员（命中 editors）可编辑', async () => {
    vi.mocked(workflowApi.getWorkflow).mockResolvedValue(definition(['u-1']));
    const { result } = renderHook(() => useWorkflowDesigner('wf-1'));
    await waitFor(() => expect(result.current.canEdit).toBe(true));
    expect(result.current.editors).toEqual(['u-1']);
  });

  it('非白名单普通成员只读且可申请', async () => {
    vi.mocked(workflowApi.getWorkflow).mockResolvedValue(definition(['u-9']));
    const { result } = renderHook(() => useWorkflowDesigner('wf-1'));
    await waitFor(() => expect(result.current.createdBy).toBe('u-2'));
    expect(result.current.canEdit).toBe(false);
    await act(async () => { await result.current.requestEditor(); });
    const { operationProposalApi } = await import('@/modules/operation-gate');
    expect(operationProposalApi.requestEditorAccess).toHaveBeenCalledWith('workflow', 'wf-1', { resourceName: 'Research' });
  });

  it('新建页（无 id）恒可编辑', async () => {
    const { result } = renderHook(() => useWorkflowDesigner());
    expect(result.current.canEdit).toBe(true);
  });
});

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { useWorkflowRunStream } from './useWorkflowRunStream';

import { streamApiGet } from '@/services/client';

vi.mock('antd', () => ({ message: { error: vi.fn() } }));
vi.mock('../api/workflow.api', () => ({ workflowApi: { getWorkflowRun: vi.fn() } }));
vi.mock('@/services/client', () => ({ streamApiGet: vi.fn() }));

const detail = {
  run: { id: 'run-1', definition_id: 'workflow-1', name: 'workflow-1', version_id: 'version-1', version: 1, status: 'running' as const, snapshot: { nodes: [], edges: [], max_concurrency: 0 }, input: {}, output: '', generation: 1, created_by: 'user-1', created_at: '', updated_at: '' },
  node_attempts: [], approvals: [], effect_intents: [], progress: { completed: 0, total: 0 }, available_actions: ['cancel' as const],
};

describe('useWorkflowRunStream', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(workflowApi.getWorkflowRun).mockResolvedValue(detail);
  });
  it('applies events and aborts the active GET stream on unmount', async () => {
    const abort = vi.fn();
    let handlers: Parameters<typeof streamApiGet>[1] | undefined;
    vi.mocked(streamApiGet).mockImplementation((_, next) => { handlers = next; return { abort } as unknown as AbortController; });
    const { result, unmount } = renderHook(() => useWorkflowRunStream('run-1'));
    await waitFor(() => expect(streamApiGet).toHaveBeenCalled());
    act(() => handlers?.onEvent({ id: '1', event: 'workflow.run_started', data: { id: 'event-1', run_id: 'run-1', sequence_no: 1, event_type: 'workflow.run_started', occurred_at: '' } }));
    expect(result.current?.lastSequence).toBe(1);
    expect(result.current?.connection).toBe('connected');
    unmount();
    expect(abort).toHaveBeenCalled();
  });

  it('reloads server-created approvals when approval is requested', async () => {
    let handlers: Parameters<typeof streamApiGet>[1] | undefined;
    vi.mocked(streamApiGet).mockImplementation((_, next) => {
      handlers = next;
      return { abort: vi.fn() } as unknown as AbortController;
    });
    vi.mocked(workflowApi.getWorkflowRun)
      .mockResolvedValueOnce(detail)
      .mockResolvedValueOnce({
        ...detail,
        run: { ...detail.run, status: 'paused', generation: 2 },
        approvals: [{
          id: 'approval-1', run_id: 'run-1', node_id: 'approval', attempt_id: 'attempt-1',
          run_generation: 2, reason: '需要审批', risk: 'medium', request_summary: '摘要',
          status: 'pending', decision_actor: '', decision_comment: '', decided_at: null,
        }],
        available_actions: ['cancel'],
      });
    const { result } = renderHook(() => useWorkflowRunStream('run-1'));
    await waitFor(() => expect(streamApiGet).toHaveBeenCalled());

    act(() => handlers?.onEvent({
      id: '2', event: 'workflow.approval_requested',
      data: { id: 'event-2', run_id: 'run-1', sequence_no: 2, event_type: 'workflow.approval_requested', occurred_at: '' },
    }));

    await waitFor(() => expect(result.current?.approvals).toHaveLength(1));
    expect(workflowApi.getWorkflowRun).toHaveBeenCalledTimes(2);
    expect(result.current?.lastSequence).toBe(2);
  });
});

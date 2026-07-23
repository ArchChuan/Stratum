import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { useWorkflowRunStream } from './useWorkflowRunStream';

import { streamApiGet } from '@/services/client';

vi.mock('antd', () => ({ message: { error: vi.fn() } }));
vi.mock('../api/workflow.api', () => ({ workflowApi: { getWorkflowRun: vi.fn() } }));
vi.mock('@/services/client', () => ({ streamApiGet: vi.fn() }));

const detail = {
  run: { id: 'run-1', definition_id: 'workflow-1', version_id: 'version-1', version: 1, status: 'running' as const, snapshot: { nodes: [], edges: [], max_concurrency: 0 }, input: {}, output: '', generation: 1, created_by: 'user-1', created_at: '', updated_at: '' },
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
});

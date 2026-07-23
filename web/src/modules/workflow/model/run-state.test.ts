import { describe, expect, it } from 'vitest';

import { createRunState, reduceRunEvent, selectCompletedCount, selectCurrentNode } from './run-state';
import type { WorkflowRunDetail, WorkflowRunEvent } from './workflow';

const detail: WorkflowRunDetail = {
  run: {
    id: 'run-1', definition_id: 'workflow-1', version_id: 'version-1', version: 1, status: 'running',
    snapshot: { nodes: [{ id: 'node-1', type: 'approval', name: '确认', agent_id: '', input_mapping: {}, output_mapping: {}, retry: { max_attempts: 0, backoff_ms: 0 }, timeout_ms: 0 }], edges: [], max_concurrency: 0 },
    input: { task: '测试' }, output: '', generation: 1, created_by: 'user-1', created_at: '', updated_at: '',
  },
  node_attempts: [{ id: 'attempt-1', run_id: 'run-1', node_id: 'node-1', attempt_no: 1, status: 'running', input: '', output_summary: '', fence_token: 1, run_generation: 1, selected_edges: [] }],
  approvals: [], effect_intents: [], progress: { completed: 0, total: 1 }, available_actions: ['cancel'],
};

const event = (sequence_no: number, event_type: string, extra: Partial<WorkflowRunEvent> = {}): WorkflowRunEvent => ({
  id: `event-${sequence_no}`, run_id: 'run-1', sequence_no, event_type, data: {}, occurred_at: '', ...extra,
});

describe('workflow run state reducer', () => {
  it('ignores duplicate and out-of-order events while updating attempts and selectors', () => {
    const initial = createRunState(detail);
    const succeeded = reduceRunEvent(initial, event(4, 'workflow.node_completed', { node_id: 'node-1', attempt_no: 1, summary: '完成' }));
    expect(succeeded.lastSequence).toBe(4);
    expect(selectCompletedCount(succeeded)).toBe(1);
    expect(selectCurrentNode(succeeded)).toBeUndefined();
    expect(reduceRunEvent(succeeded, event(4, 'workflow.node_completed'))).toBe(succeeded);
    expect(reduceRunEvent(succeeded, event(3, 'workflow.run_started'))).toBe(succeeded);
  });

  it('appends bounded output and records tool steps separately from connection status', () => {
    let state = createRunState(detail);
    state = reduceRunEvent(state, event(1, 'workflow.node_output_delta', { node_id: 'node-1', data: { delta: '输出片段' } }));
    state = reduceRunEvent(state, event(2, 'workflow.node_tool_step', { node_id: 'node-1', data: { tool: 'search', latency_ms: 18, summary: '完成检索' } }));
    expect(state.outputByNode['node-1']).toBe('输出片段');
    expect(state.toolStepsByNode['node-1'][0]).toMatchObject({ tool: 'search', latency_ms: 18 });
    expect(state.connection).toBe('offline');
    expect(state.run.status).toBe('running');
  });
});

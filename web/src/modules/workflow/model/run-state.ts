import type { WorkflowNodeAttempt, WorkflowRun, WorkflowRunDetail, WorkflowRunEvent } from './workflow';

import { WORKFLOW_OUTPUT_MAX_CHARS } from '@/constants';

export type RunConnectionStatus = 'connected' | 'reconnecting' | 'offline';
export interface WorkflowToolStep { tool?: string; latency_ms?: number; summary?: string; [key: string]: unknown }
export interface WorkflowRunState {
  run: WorkflowRun;
  attempts: WorkflowNodeAttempt[];
  approvals: WorkflowRunDetail['approvals'];
  effectIntents: WorkflowRunDetail['effect_intents'];
  availableActions: WorkflowRunDetail['available_actions'];
  lastSequence: number;
  outputByNode: Record<string, string>;
  toolStepsByNode: Record<string, WorkflowToolStep[]>;
  connection: RunConnectionStatus;
}

export const createRunState = (detail: WorkflowRunDetail): WorkflowRunState => ({
  run: detail.run,
  attempts: detail.node_attempts,
  approvals: detail.approvals,
  effectIntents: detail.effect_intents,
  availableActions: detail.available_actions,
  lastSequence: 0,
  outputByNode: Object.fromEntries(detail.node_attempts.filter((attempt) => attempt.output_summary).map((attempt) => [attempt.node_id, attempt.output_summary])),
  toolStepsByNode: {},
  connection: 'offline',
});

const attemptStatusFor = (eventType: string): WorkflowNodeAttempt['status'] | undefined => ({
  'workflow.node_started': 'running',
  'workflow.node_completed': 'succeeded',
  'workflow.node_failed': 'failed',
  'workflow.node_retrying': 'retry_wait',
  'workflow.node_skipped': 'skipped',
  'workflow.node_paused': 'paused',
  'workflow.node_canceled': 'canceled',
  'workflow.manual_intervention': 'manual_intervention',
}[eventType] as WorkflowNodeAttempt['status'] | undefined);

const runStatusFor = (eventType: string): WorkflowRun['status'] | undefined => ({
  'workflow.run_started': 'running', 'workflow.run_completed': 'completed', 'workflow.run_failed': 'failed',
  'workflow.paused': 'paused', 'workflow.pause_requested': 'pause_requested', 'workflow.cancel_requested': 'cancel_requested',
  'workflow.canceled': 'canceled', 'workflow.manual_intervention': 'manual_intervention', 'workflow.resumed': 'queued',
}[eventType] as WorkflowRun['status'] | undefined);

export const reduceRunEvent = (state: WorkflowRunState, event: WorkflowRunEvent): WorkflowRunState => {
  if (event.sequence_no <= state.lastSequence) return state;
  let next: WorkflowRunState = { ...state, lastSequence: event.sequence_no };
  const runStatus = runStatusFor(event.event_type);
  if (runStatus) next = { ...next, run: { ...next.run, status: runStatus } };
  const attemptStatus = attemptStatusFor(event.event_type);
  if (attemptStatus && event.node_id) {
    next = {
      ...next,
      attempts: next.attempts.map((attempt) => attempt.node_id === event.node_id && (!event.attempt_no || attempt.attempt_no === event.attempt_no)
        ? { ...attempt, status: attemptStatus, output_summary: event.summary || attempt.output_summary, error_message: event.event_type === 'workflow.node_failed' ? event.summary : attempt.error_message }
        : attempt),
    };
  }
  if (event.event_type === 'workflow.node_output_delta' && event.node_id) {
    const delta = typeof event.data.delta === 'string' ? event.data.delta : '';
    const output = `${next.outputByNode[event.node_id] || ''}${delta}`.slice(-WORKFLOW_OUTPUT_MAX_CHARS);
    next = { ...next, outputByNode: { ...next.outputByNode, [event.node_id]: output } };
  }
  if (event.event_type === 'workflow.node_tool_step' && event.node_id) {
    next = { ...next, toolStepsByNode: { ...next.toolStepsByNode, [event.node_id]: [...(next.toolStepsByNode[event.node_id] || []), event.data] } };
  }
  return next;
};

export const selectCompletedCount = (state: WorkflowRunState) => state.attempts.filter((attempt) => attempt.status === 'succeeded' || attempt.status === 'skipped').length;
export const selectCurrentNode = (state: WorkflowRunState) => state.attempts.find((attempt) => attempt.status === 'running');
export const selectFailedNode = (state: WorkflowRunState) => state.attempts.find((attempt) => attempt.status === 'failed');
export const selectPendingApproval = (state: WorkflowRunState) => state.approvals.find((approval) => approval.status === 'pending');

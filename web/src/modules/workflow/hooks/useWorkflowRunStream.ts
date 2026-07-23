import { message } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import { createRunState, reduceRunEvent, type WorkflowRunState } from '../model/run-state';
import { workflowRunEventSchema } from '../model/workflow';

import { WORKFLOW_STREAM_RECONNECT_BASE_MS, WORKFLOW_STREAM_RECONNECT_MAX_MS } from '@/constants';
import { streamApiGet } from '@/services/client';

const terminal = new Set(['completed', 'failed', 'canceled']);

export const useWorkflowRunStream = (runId: string) => {
  const [state, setState] = useState<WorkflowRunState | null>(null);
  const stateRef = useRef<WorkflowRunState | null>(null);
  const reconnectDelay = useRef(WORKFLOW_STREAM_RECONNECT_BASE_MS);

  useEffect(() => {
    let disposed = false;
    let controller: AbortController | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const update = (next: WorkflowRunState) => { stateRef.current = next; setState(next); };

    const connect = () => {
      const current = stateRef.current;
      if (!current || terminal.has(current.run.status) || disposed) return;
      update({ ...current, connection: current.lastSequence ? 'reconnecting' : 'offline' });
      controller = streamApiGet(`/workflow-runs/${runId}/events/stream`, {
        lastEventId: current.lastSequence ? String(current.lastSequence) : undefined,
        onEvent: (envelope) => {
          const parsed = workflowRunEventSchema.safeParse(envelope.data);
          if (!parsed.success || !stateRef.current) return;
          reconnectDelay.current = WORKFLOW_STREAM_RECONNECT_BASE_MS;
          update({ ...reduceRunEvent(stateRef.current, parsed.data), connection: 'connected' });
        },
        onClose: () => scheduleReconnect(),
        onError: () => scheduleReconnect(),
      });
    };
    const scheduleReconnect = () => {
      const current = stateRef.current;
      if (!current || terminal.has(current.run.status) || disposed) return;
      update({ ...current, connection: 'reconnecting' });
      const delay = reconnectDelay.current;
      reconnectDelay.current = Math.min(delay * 2, WORKFLOW_STREAM_RECONNECT_MAX_MS);
      timer = setTimeout(connect, delay);
    };

    workflowApi.getWorkflowRun(runId).then((detail) => {
      if (disposed) return;
      update(createRunState(detail));
      connect();
    }).catch(() => { if (!disposed) message.error({ content: '操作失败', duration: 0 }); });

    return () => { disposed = true; controller?.abort(); if (timer) clearTimeout(timer); };
  }, [runId]);

  return state;
};

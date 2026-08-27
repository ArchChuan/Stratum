import { message } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowVersion } from '../model/workflow';

import { createIdempotencyKey } from './idempotencyKey';

interface RequestError { response?: { data?: { error?: string } } }

export const useWorkflowExecution = (workflowId: string) => {
  const [version, setVersion] = useState<WorkflowVersion | null>(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const idempotencyKey = useRef(createIdempotencyKey());

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        // 优先取生效版本：回退后 active_version_id 指回历史版本，不再是最新版本。
        const definition = await workflowApi.getWorkflow(workflowId);
        let preferredId = definition.active_version_id;
        if (!preferredId) {
          const page = await workflowApi.listWorkflowVersions(workflowId, { page: 1, pageSize: 1 });
          preferredId = page.versions[0]?.id;
        }
        if (!preferredId) throw new Error('这个工作流还没有可运行的发布版本');
        return workflowApi.getWorkflowVersion(workflowId, preferredId);
      } catch (error: unknown) {
        if (!cancelled) message.error({ content: (error as RequestError).response?.data?.error || (error as Error).message || '操作失败', duration: 3 });
        return null;
      }
    };
    load().then((next) => { if (!cancelled && next) setVersion(next); }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [workflowId]);

  const start = async (input: { task: string; fields: Record<string, unknown> }) => {
    if (!version || submitting) return null;
    setSubmitting(true);
    try {
      const result = await workflowApi.startWorkflowRun({ version_id: version.id, ...input, idempotency_key: idempotencyKey.current });
      idempotencyKey.current = createIdempotencyKey();
      return result;
    } catch (error: unknown) {
      message.error({ content: (error as RequestError).response?.data?.error || '操作失败', duration: 3 });
      return null;
    } finally {
      setSubmitting(false);
    }
  };

  return { version, loading, submitting, start };
};

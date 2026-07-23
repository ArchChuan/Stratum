import { message } from 'antd';
import { useEffect, useRef, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowVersion } from '../model/workflow';

interface RequestError { response?: { data?: { error?: string } } }

export const useWorkflowExecution = (workflowId: string) => {
  const [version, setVersion] = useState<WorkflowVersion | null>(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const idempotencyKey = useRef(crypto.randomUUID());

  useEffect(() => {
    let cancelled = false;
    workflowApi.listWorkflowVersions(workflowId, { page: 1, pageSize: 1 }).then(async (page) => {
      if (!page.versions[0]) throw new Error('这个工作流还没有可运行的发布版本');
      return workflowApi.getWorkflowVersion(workflowId, page.versions[0].id);
    }).then((next) => { if (!cancelled) setVersion(next); }).catch((error: unknown) => {
      if (!cancelled) message.error({ content: (error as RequestError).response?.data?.error || (error as Error).message || '操作失败', duration: 0 });
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [workflowId]);

  const start = async (input: { task: string; fields: Record<string, unknown> }) => {
    if (!version || submitting) return null;
    setSubmitting(true);
    try {
      const result = await workflowApi.startWorkflowRun({ version_id: version.id, ...input, idempotency_key: idempotencyKey.current });
      idempotencyKey.current = crypto.randomUUID();
      return result;
    } catch (error: unknown) {
      message.error({ content: (error as RequestError).response?.data?.error || '操作失败', duration: 0 });
      return null;
    } finally {
      setSubmitting(false);
    }
  };

  return { version, loading, submitting, start };
};

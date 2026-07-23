import { message } from 'antd';
import { useEffect, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowRunStatus, WorkflowRunSummary } from '../model/workflow';

import { WORKFLOW_DEFAULT_PAGE_SIZE } from '@/constants';

interface RequestError { response?: { data?: { error?: string } } }

export const useWorkflowRuns = () => {
  const [runs, setRuns] = useState<WorkflowRunSummary[]>([]);
  const [definitionId, setDefinitionId] = useState('');
  const [status, setStatus] = useState<WorkflowRunStatus | ''>('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(WORKFLOW_DEFAULT_PAGE_SIZE);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    workflowApi.listWorkflowRuns({ definitionId, status, page, pageSize }).then((result) => {
      if (cancelled) return;
      setRuns(result.runs);
      setTotal(result.total);
    }).catch((error: unknown) => {
      if (!cancelled) message.error({ content: (error as RequestError).response?.data?.error || '操作失败', duration: 0 });
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [definitionId, page, pageSize, status]);

  return {
    runs, definitionId, status, page, pageSize, total, loading,
    setDefinitionId: (value: string) => { setDefinitionId(value); setPage(1); },
    setStatus: (value: WorkflowRunStatus | '') => { setStatus(value); setPage(1); },
    setPage,
    setPageSize,
  };
};

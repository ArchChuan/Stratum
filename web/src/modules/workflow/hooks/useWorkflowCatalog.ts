import { message } from 'antd';
import { useEffect, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowSummary } from '../model/workflow';

import { WORKFLOW_DEFAULT_PAGE_SIZE } from '@/constants';

export const useWorkflowCatalog = () => {
  const [workflows, setWorkflows] = useState<WorkflowSummary[]>([]);
  const [query, setQuery] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(WORKFLOW_DEFAULT_PAGE_SIZE);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    workflowApi.listWorkflows({ query, page, pageSize })
      .then((result) => {
        if (cancelled) return;
        setWorkflows(result.workflows);
        setTotal(result.total);
      })
      .catch((err) => {
        if (cancelled) return;
        message.error({ content: err.response?.data?.error || '加载工作流失败', duration: 0 });
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => { cancelled = true; };
  }, [page, pageSize, query]);

  const search = (value: string) => {
    setQuery(value);
    setPage(1);
  };

  return { workflows, query, search, page, pageSize, total, loading, setPage, setPageSize };
};

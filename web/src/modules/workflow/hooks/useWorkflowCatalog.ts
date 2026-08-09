import { message } from 'antd';
import { useEffect, useState } from 'react';

import { workflowApi } from '../api/workflow.api';
import type { WorkflowSummary } from '../model/workflow';

import { WORKFLOW_DEFAULT_PAGE_SIZE } from '@/constants';
import { extractErrorMessage, isForbidden } from '@/shared/lib';

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
        if (cancelled || isForbidden(err)) return;
        message.error({ content: extractErrorMessage(err, '加载工作流失败'), duration: 0 });
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

  const deleteWorkflow = async (workflowId: string) => {
    try {
      await workflowApi.deleteWorkflow(workflowId);
      message.success({ content: '工作流草稿已删除', duration: 2 });
      setWorkflows((current) => current.filter((workflow) => workflow.id !== workflowId));
      setTotal((current) => Math.max(0, current - 1));
    } catch (err: unknown) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '删除工作流失败'), duration: 0 });
      }
    }
  };

  return { workflows, query, search, page, pageSize, total, loading, deleteWorkflow, setPage, setPageSize };
};

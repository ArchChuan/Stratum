import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { agentApi } from '../api/agent.api';
import type { ExecutionRow } from '../model/execution';

import { DEFAULT_PAGE_SIZE } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';

interface PaginationState {
  current: number;
  pageSize: number;
  total: number;
}

export const useExecutionHistory = () => {
  const [executions, setExecutions] = useState<ExecutionRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [pagination, setPagination] = useState<PaginationState>({
    current: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    total: 0,
  });

  const fetchPage = useCallback(async (page: number, pageSize: number) => {
    setLoading(true);
    try {
      const res = await agentApi.executions(page, pageSize);
      const data = res.data as { executions?: ExecutionRow[]; total?: number };
      setExecutions(data.executions || []);
      setPagination({ current: page, pageSize, total: data.total || 0 });
    } catch (err) {
      message.error(extractErrorMessage(err) || '加载执行历史失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const res = await agentApi.executions(1, DEFAULT_PAGE_SIZE);
        const data = res.data as { executions?: ExecutionRow[]; total?: number };
        if (!cancelled) {
          setExecutions(data.executions || []);
          setPagination({ current: 1, pageSize: DEFAULT_PAGE_SIZE, total: data.total || 0 });
        }
      } catch (err) {
        if (!cancelled) message.error(extractErrorMessage(err) || '加载执行历史失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return { executions, loading, pagination, fetchPage };
};

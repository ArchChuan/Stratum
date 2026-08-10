import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { dashboardApi } from '../api/dashboard.api';
import type { DashboardCounts, DashboardExecution } from '../model/dashboard';

import { COMPACT_PAGE_SIZE } from '@/constants';
import { usePagination } from '@/shared/hooks';

const initialCounts: DashboardCounts = {
  agents: 0,
  skills: 0,
  knowledge_workspaces: 0,
  mcp_servers: 0,
  model_providers: 0,
  tenant_members: 0,
  workflows: 0,
  agent_user_messages_7d: 0,
};

export const useDashboardPage = () => {
  const [counts, setCounts] = useState<DashboardCounts>(initialCounts);
  const [executions, setExecutions] = useState<DashboardExecution[]>([]);
  const [loading, setLoading] = useState(true);
  const [executionsLoading, setExecutionsLoading] = useState(false);
  // 翻页请求序号：快速翻页时丢弃过期响应，避免旧页数据覆盖新页。
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange } = usePagination({ pageSize: COMPACT_PAGE_SIZE });

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      // 概览与执行表互不依赖，并行拉取；单侧失败不影响另一侧展示。
      const [overviewResult, executionsResult] = await Promise.allSettled([
        dashboardApi.overview(),
        dashboardApi.executions({ page: 1, pageSize: COMPACT_PAGE_SIZE }),
      ]);
      if (cancelled) return;
      if (overviewResult.status === 'fulfilled') {
        setCounts(overviewResult.value);
      } else {
        message.error({ content: '加载概览数据失败', duration: 0 });
      }
      if (executionsResult.status === 'fulfilled') {
        setExecutions(executionsResult.value.executions);
        setTotal(executionsResult.value.total);
      } else {
        message.error({ content: '加载执行记录失败', duration: 0 });
      }
      setLoading(false);
    })();
    return () => { cancelled = true; };
  }, [setTotal]);

  const loadExecutions = useCallback(async (nextPage: number, nextPageSize: number) => {
    const seq = ++requestSeqRef.current;
    setExecutionsLoading(true);
    try {
      const pageData = await dashboardApi.executions({ page: nextPage, pageSize: nextPageSize });
      if (seq !== requestSeqRef.current) return;
      setExecutions(pageData.executions);
      setTotal(pageData.total);
    } catch {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: '加载执行记录失败', duration: 0 });
    } finally {
      if (seq === requestSeqRef.current) setExecutionsLoading(false);
    }
  }, [setTotal]);

  const handlePageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void loadExecutions(nextPage, nextPageSize);
  }, [onChange, loadExecutions]);

  return {
    counts,
    loading,
    executions,
    executionsTotal: total,
    executionsLoading,
    page,
    pageSize,
    handlePageChange,
  };
};

export default useDashboardPage;

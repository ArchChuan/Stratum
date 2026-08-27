import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemorySummary } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useSummariesTab = () => {
  const [summaries, setSummaries] = useState<MemorySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listSummaries({ page, page_size: pageSize });
      if (seq !== requestSeqRef.current) return;
      setSummaries(data.summaries);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载摘要失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const deleteSummary = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteSummary(id);
      message.success({ content: '摘要已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除摘要失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { summaries, loading, deleteLoading, deleteSummary, pagination: { current: page, pageSize, total, pageSizeOptions, onChange }, reload: load };
};

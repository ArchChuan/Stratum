import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryFact, MemoryStats } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useMyMemoriesPage = () => {
  const [memories, setMemories] = useState<MemoryFact[]>([]);
  const [loading, setLoading] = useState(true);
  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState<string | null>(null);
  const [clearLoading, setClearLoading] = useState(false);
  // 请求序号：翻页时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async (nextPage: number, nextPageSize: number) => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const pageData = await memoryUserApi.listMyMemories({ page: nextPage, pageSize: nextPageSize });
      if (seq !== requestSeqRef.current) return;
      setMemories(pageData.memories);
      setTotal(pageData.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载记忆失败', duration: 0 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [setTotal]);

  const loadStats = useCallback(async () => {
    setStatsLoading(true);
    try {
      const next = await memoryUserApi.getStats();
      setStats(next);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载记忆统计失败', duration: 0 });
    } finally {
      setStatsLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(1, pageSize);
    void loadStats();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅首次加载
  }, []);

  const handlePageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void load(nextPage, nextPageSize);
  }, [onChange, load]);

  const handleDelete = useCallback(async (id: string) => {
    setDeleteLoading(id);
    try {
      await memoryUserApi.deleteMemory(id);
      message.success({ content: '记忆已删除', duration: 2 });
      await load(page, pageSize);
      void loadStats();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除记忆失败', duration: 0 });
    } finally {
      setDeleteLoading(null);
    }
  }, [page, pageSize, load, loadStats]);

  const handleClearAll = useCallback(async () => {
    setClearLoading(true);
    try {
      await memoryUserApi.clearMyMemories();
      message.success({ content: '已清空全部记忆', duration: 2 });
      await load(1, pageSize);
      void loadStats();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '清空记忆失败', duration: 0 });
    } finally {
      setClearLoading(false);
    }
  }, [pageSize, load, loadStats]);

  return {
    memories,
    loading,
    stats,
    statsLoading,
    deleteLoading,
    clearLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    handlePageChange,
    handleDelete,
    handleClearAll,
  };
};

export default useMyMemoriesPage;

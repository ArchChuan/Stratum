import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryEntity, MemoryFact, MemoryStats } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useMyMemoriesPage = () => {
  const [memories, setMemories] = useState<MemoryFact[]>([]);
  const [loading, setLoading] = useState(true);
  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(true);
  const [clearLoading, setClearLoading] = useState(false);
  const [entities, setEntities] = useState<MemoryEntity[]>([]);
  const [entitiesLoading, setEntitiesLoading] = useState(true);
  const [entityTotal, setEntityTotal] = useState(0);
  // 请求序号：翻页时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const entityRequestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();
  const {
    current: entityPage,
    pageSize: entityPageSize,
    pageSizeOptions: entityPageSizeOptions,
    onChange: onEntityChange,
  } = usePagination();

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

  const loadEntities = useCallback(async (nextPage: number, nextPageSize: number) => {
    const seq = ++entityRequestSeqRef.current;
    setEntitiesLoading(true);
    try {
      const pageData = await memoryUserApi.listMyEntities({ page: nextPage, pageSize: nextPageSize });
      if (seq !== entityRequestSeqRef.current) return;
      setEntities(pageData.entities);
      setEntityTotal(pageData.total);
    } catch (err) {
      if (seq !== entityRequestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载记忆实体失败', duration: 0 });
    } finally {
      if (seq === entityRequestSeqRef.current) setEntitiesLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(1, pageSize);
    void loadStats();
    void loadEntities(1, entityPageSize);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅首次加载
  }, []);

  const handlePageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void load(nextPage, nextPageSize);
  }, [onChange, load]);

  const handleEntityPageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onEntityChange(nextPage, nextPageSize);
    void loadEntities(nextPage, nextPageSize);
  }, [onEntityChange, loadEntities]);

  const handleClearAll = useCallback(async () => {
    setClearLoading(true);
    try {
      await memoryUserApi.clearMyMemories();
      message.success({ content: '已清空全部记忆', duration: 2 });
      await load(1, pageSize);
      void loadStats();
      await loadEntities(1, entityPageSize);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '清空记忆失败', duration: 0 });
    } finally {
      setClearLoading(false);
    }
  }, [pageSize, entityPageSize, load, loadStats, loadEntities]);

  return {
    memories,
    loading,
    stats,
    statsLoading,
    clearLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    entities,
    entitiesLoading,
    entityTotal,
    entityPage,
    entityPageSize,
    entityPageSizeOptions,
    handlePageChange,
    handleEntityPageChange,
    handleClearAll,
  };
};

export default useMyMemoriesPage;

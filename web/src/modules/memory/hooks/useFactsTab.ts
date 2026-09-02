import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryFact } from '../model/memory';

import { usePagination } from '@/shared/hooks';
import { extractErrorMessage } from '@/shared/lib';

export interface FactFilters {
  q?: string;
  importanceMin?: number;
  importanceMax?: number;
  category?: string;
}

export const useFactsTab = () => {
  const [facts, setFacts] = useState<MemoryFact[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [filters, setFilters] = useState<FactFilters>({});
  // 请求序号：筛选/翻页时丢弃过期响应，避免旧数据覆盖新数据。
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listFacts({
        page,
        page_size: pageSize,
        ...(filters.q ? { q: filters.q } : {}),
        ...(filters.importanceMin !== undefined ? { importance_min: filters.importanceMin } : {}),
        ...(filters.importanceMax !== undefined ? { importance_max: filters.importanceMax } : {}),
        ...(filters.category ? { category: filters.category } : {}),
      });
      if (seq !== requestSeqRef.current) return;
      setFacts(data.facts);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: extractErrorMessage(err, '加载事实失败'), duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, filters, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const applyFilters = useCallback((next: FactFilters) => {
    setFilters(next);
    // 筛选变化后回到第 1 页，避免用越界页码请求导致列表空态被误判为无结果。
    onChange(1, pageSize);
  }, [onChange, pageSize]);

  const deleteFact = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteFact(id);
      message.success({ content: '事实已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '删除事实失败'), duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  const updateFact = useCallback(async (id: string, patch: { content?: string; importance?: number; category?: string }) => {
    try {
      const res = await memoryUserApi.updateFact(id, patch);
      if (res.vector_sync_failed) {
        // spec §4：内容已保存，向量同步失败待后台补偿。
        message.error({ content: '内容已保存，但向量同步失败，将在后台补偿', duration: 3 });
      } else {
        message.success({ content: '事实已更新', duration: 2 });
      }
      await load();
      return res.fact;
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新事实失败'), duration: 3 });
      throw err;
    }
  }, [load]);

  return { facts, loading, deleteLoading, filters, applyFilters, updateFact, deleteFact, pagination: { current: page, pageSize, total, pageSizeOptions, onChange }, reload: load };
};

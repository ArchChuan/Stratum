import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryEntryItem } from '../model/memory';

import { usePagination } from '@/shared/hooks';
import { extractErrorMessage } from '@/shared/lib';

export const useEntriesTab = () => {
  const [entries, setEntries] = useState<MemoryEntryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [query, setQuery] = useState('');
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listEntries({ page, page_size: pageSize, q: query || undefined });
      if (seq !== requestSeqRef.current) return;
      setEntries(data.entries);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: extractErrorMessage(err, '加载条目失败'), duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, query, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const deleteEntry = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteEntry(id);
      message.success({ content: '条目已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '删除条目失败'), duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { entries, loading, deleteLoading, query, setQuery, deleteEntry, pagination: { current: page, pageSize, total, pageSizeOptions, onChange }, reload: load };
};

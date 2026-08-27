import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryEntity } from '../model/memory';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export const useEntitiesTab = () => {
  const [entities, setEntities] = useState<MemoryEntity[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const requestSeqRef = useRef(0);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const data = await memoryUserApi.listMyEntities({ page, pageSize });
      if (seq !== requestSeqRef.current) return;
      setEntities(data.entities);
      setTotal(data.total);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载实体失败', duration: 3 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const deleteEntity = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await memoryUserApi.deleteEntity(id);
      message.success({ content: '实体已删除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '删除实体失败', duration: 3 });
    } finally {
      setDeleteLoading(false);
    }
  }, [load]);

  return { entities, loading, deleteLoading, deleteEntity, pagination: { current: page, pageSize, total, pageSizeOptions, onChange } };
};

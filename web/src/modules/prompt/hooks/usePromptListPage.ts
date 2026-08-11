import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { promptApi } from '../api/prompt.api';
import type { PromptSummary } from '../model/prompt';

import { usePagination } from '@/shared/hooks/usePagination';

interface RequestError { response?: { data?: { error?: string } } }

export const usePromptListPage = () => {
  const [prompts, setPrompts] = useState<PromptSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();
  // 请求序号防竞态：翻页/刷新交错时丢弃过期响应。
  const requestSeqRef = useRef(0);

  const load = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    setLoading(true);
    try {
      const { prompts: items, total: totalCount } = await promptApi.listPrompts({ page, pageSize });
      if (seq !== requestSeqRef.current) return; // 过期响应
      setPrompts(items);
      setTotal(totalCount);
    } catch (err) {
      if (seq !== requestSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载提示词模板失败', duration: 0 });
    } finally {
      if (seq === requestSeqRef.current) setLoading(false);
    }
  }, [page, pageSize, setTotal]);

  useEffect(() => {
    void load();
  }, [load]);

  const handlePageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
  }, [onChange]);

  const handleCreate = useCallback(async (key: string, content: string) => {
    setCreateLoading(true);
    try {
      await promptApi.create({ key, content });
      message.success({ content: '模板已创建', duration: 2 });
      setCreateOpen(false);
      // 回到第一页并刷新，保证新 key 可见。
      if (page !== 1) {
        onChange(1, pageSize);
      } else {
        await load();
      }
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '创建模板失败', duration: 0 });
    } finally {
      setCreateLoading(false);
    }
  }, [load, onChange, page, pageSize]);

  return {
    prompts,
    loading,
    createOpen,
    createLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    handlePageChange,
    openCreate: () => setCreateOpen(true),
    closeCreate: () => setCreateOpen(false),
    handleCreate,
    reload: () => void load(),
  };
};

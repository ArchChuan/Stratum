import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { llmApi } from '../api/llm.api';
import type { Provider, CreateProviderInput } from '../model/llm';

import { extractErrorMessage } from '@/shared/lib';

export function useProviders() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const fetch = useCallback(async () => {
    setLoading(true);
    try {
      const data = await llmApi.listProviders();
      setProviders(data);
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '加载厂商列表失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const data = await llmApi.listProviders();
        if (!cancelled) setProviders(data);
      } catch (err) {
        if (!cancelled) message.error({ content: extractErrorMessage(err) || '加载厂商列表失败', duration: 0 });
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const createProvider = useCallback(async (values: CreateProviderInput) => {
    setCreateLoading(true);
    try {
      await llmApi.createProvider(values);
      message.success({ content: '厂商已创建', duration: 2 });
      await fetch();
    } catch (err: any) {
      message.error({ content: err.response?.data?.error || '创建厂商失败', duration: 0 });
    } finally {
      setCreateLoading(false);
    }
  }, [fetch]);

  const deleteProvider = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await llmApi.deleteProvider(id);
      message.success({ content: '厂商已删除', duration: 2 });
      await fetch();
    } catch (err: any) {
      message.error({ content: err.response?.data?.error || '删除厂商失败', duration: 0 });
    } finally {
      setDeleteLoading(false);
    }
  }, [fetch]);

  return { providers, loading, createLoading, deleteLoading, refresh: fetch, createProvider, deleteProvider };
}

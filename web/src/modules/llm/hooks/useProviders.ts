import { useCallback, useEffect, useState } from 'react';
import { message } from 'antd';

import { llmApi } from '../api/llm.api';
import type { Provider, CreateProviderInput } from '../model/llm';

import { extractErrorMessage } from '@/shared/lib';

export function useProviders() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(false);

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
    await llmApi.createProvider(values);
    message.success({ content: '厂商已创建', duration: 2 });
    await fetch();
  }, [fetch]);

  const deleteProvider = useCallback(async (id: string) => {
    await llmApi.deleteProvider(id);
    message.success({ content: '厂商已删除', duration: 2 });
    setProviders((prev) => prev.filter((p) => p.id !== id));
  }, []);

  return { providers, loading, refresh: fetch, createProvider, deleteProvider };
}

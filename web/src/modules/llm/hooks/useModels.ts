import { useCallback, useEffect, useState } from 'react';
import { message } from 'antd';

import { llmApi } from '../api/llm.api';
import type { Model, UpdateModelInput } from '../model/llm';

import { extractErrorMessage } from '@/shared/lib';

export function useModels() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(false);

  const fetch = useCallback(async () => {
    setLoading(true);
    try {
      const data = await llmApi.listModels();
      setModels(data);
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '加载模型列表失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const data = await llmApi.listModels();
        if (!cancelled) setModels(data);
      } catch (err) {
        if (!cancelled) message.error({ content: extractErrorMessage(err) || '加载模型列表失败', duration: 0 });
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, []);

  const toggleModel = useCallback(async (id: string, enabled: boolean) => {
    try {
      await llmApi.toggleModel(id, enabled);
      setModels((prev) => prev.map((m) => (m.id === id ? { ...m, enabled } : m)));
      message.success({ content: '已更新', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '更新失败', duration: 0 });
    }
  }, []);

  const updateModel = useCallback(async (id: string, values: UpdateModelInput) => {
    await llmApi.updateModel(id, values);
    message.success({ content: '模型已更新', duration: 2 });
    await fetch();
  }, [fetch]);

  const deleteModel = useCallback(async (id: string) => {
    await llmApi.deleteModel(id);
    message.success({ content: '模型已删除', duration: 2 });
    setModels((prev) => prev.filter((m) => m.id !== id));
  }, []);

  return { models, loading, refresh: fetch, toggleModel, updateModel, deleteModel };
}

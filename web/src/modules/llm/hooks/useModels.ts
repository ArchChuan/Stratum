import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { llmApi } from '../api/llm.api';
import type { Model, UpdateModelInput } from '../model/llm';

import { extractErrorMessage } from '@/shared/lib';

export function useModels() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const fetch = useCallback(async () => {
    setLoading(true);
    try {
      const data = await llmApi.listModels();
      setModels(data);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载模型列表失败'), duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetch();
  }, [fetch]);

  const toggleModel = useCallback(async (id: string, enabled: boolean) => {
    try {
      await llmApi.toggleModel(id, enabled);
      setModels((prev) => prev.map((m) => (m.id === id ? { ...m, enabled } : m)));
      message.success({ content: '已更新', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新失败'), duration: 0 });
    }
  }, []);

  const updateModel = useCallback(async (id: string, values: UpdateModelInput) => {
    try {
      await llmApi.updateModel(id, values);
      message.success({ content: '模型已更新', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新模型失败'), duration: 0 });
    }
  }, [fetch]);

  // 设置/取消默认嵌入会连带清除其他模型的标记，成功后整体刷新。
  const setDefaultEmbedding = useCallback(async (id: string, enabled: boolean) => {
    try {
      await llmApi.setDefaultEmbedding(id, enabled);
      message.success({ content: enabled ? '已设为默认嵌入' : '已取消默认嵌入', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '设置默认嵌入失败'), duration: 0 });
    }
  }, [fetch]);

  const deleteModel = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await llmApi.deleteModel(id);
      message.success({ content: '模型已删除', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '删除模型失败'), duration: 0 });
    } finally {
      setDeleteLoading(false);
    }
  }, [fetch]);

  return {
    models,
    loading,
    deleteLoading,
    refresh: fetch,
    toggleModel,
    updateModel,
    deleteModel,
    setDefaultEmbedding,
  };
}

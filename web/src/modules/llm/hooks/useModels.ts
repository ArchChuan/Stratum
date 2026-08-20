import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { llmApi } from '../api/llm.api';
import type { Model, UpdateModelInput, UpdateModelPolicyInput } from '../model/llm';

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
      message.error({ content: extractErrorMessage(err, '加载模型列表失败'), duration: 3 });
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
      message.error({ content: extractErrorMessage(err, '更新失败'), duration: 3 });
    }
  }, []);

  const updateModel = useCallback(async (id: string, values: UpdateModelInput) => {
    try {
      await llmApi.updateModel(id, values);
      message.success({ content: '模型已更新', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新模型失败'), duration: 3 });
    }
  }, [fetch]);

  const updateModelPolicy = useCallback(async (id: string, values: UpdateModelPolicyInput) => {
    try {
      await llmApi.updateModelPolicy(id, values);
      message.success({ content: '运行策略已更新', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新运行策略失败'), duration: 3 });
    }
  }, [fetch]);

  const deleteModel = useCallback(async (id: string) => {
    setDeleteLoading(true);
    try {
      await llmApi.deleteModel(id);
      message.success({ content: '模型已删除', duration: 2 });
      await fetch();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '删除模型失败'), duration: 3 });
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
    updateModelPolicy,
    deleteModel,
  };
}

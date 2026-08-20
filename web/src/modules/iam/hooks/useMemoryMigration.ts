import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { memoryMigrationApi } from '../api/memory-migration.api';
import { tenantApi } from '../api/tenant.api';

import { MEMORY_MIGRATION_POLL_MS } from '@/constants';
import { llmApi } from '@/modules/llm';
import type { MemoryMigrationCostResponse, MemoryMigrationResponse } from '@/services/gen/memory';
import { extractErrorMessage } from '@/shared/lib';


// 迁移卡片可切换的目标模型目录项。iam 模块被 lint 禁止依赖业务模块（agent），
// 故不复用 agent 的 buildGroupedModels，仅内联卡片实际用到的 value/label/health。
export interface MigrationModelOption {
  value: string;
  label: string;
  health?: string;
}
export interface MigrationModelGroup {
  provider: string;
  models: MigrationModelOption[];
}

export interface UseMemoryMigrationResult {
  migration: MemoryMigrationResponse | null;
  loading: boolean;
  // 当前生效嵌入模型：租户 settings.memory_embedding_model（迁移启动即切换，恒与
  // 最近一次迁移的 to_model 一致）；未配置为空串。
  currentModel: string;
  models: MigrationModelGroup[];
  modelsLoading: boolean;
  targetModel: string | undefined;
  setTargetModel: (model?: string) => void;
  cost: MemoryMigrationCostResponse | null;
  costLoading: boolean;
  starting: boolean;
  canceling: boolean;
  retrying: boolean;
  fetchCost: () => Promise<MemoryMigrationCostResponse | null>;
  startMigration: (toModel: string) => Promise<void>;
  cancelMigration: (id: number) => Promise<void>;
  retryMigration: (id: number) => Promise<void>;
}

// embedding 模型按 provider 分组（仅保留卡片展示所需字段）。
const groupEmbeddingModels = (
  models: Array<{ providerId: string; name: string; displayName?: string; health?: string }>,
  providers: Array<{ id: string; name: string }>,
): MigrationModelGroup[] => {
  const providerName = new Map(providers.map((p) => [p.id, p.name]));
  const grouped = new Map<string, MigrationModelOption[]>();
  for (const m of models) {
    const provider = providerName.get(m.providerId) || m.providerId;
    if (!grouped.has(provider)) grouped.set(provider, []);
    grouped.get(provider)!.push({ value: m.name, label: m.displayName || m.name, health: m.health });
  }
  return Array.from(grouped.entries()).map(([provider, modelOptions]) => ({
    provider,
    models: modelOptions,
  }));
};

// 记忆嵌入模型平滑迁移（P5）状态与动作：当前迁移、生效模型、可切换目标模型目录、
// 成本预览、确认制启动与取消/重试。migrating 期间后台轮询进度。
export function useMemoryMigration(): UseMemoryMigrationResult {
  const [migration, setMigration] = useState<MemoryMigrationResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [currentModel, setCurrentModel] = useState('');
  const [models, setModels] = useState<MigrationModelGroup[]>([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const [targetModel, setTargetModel] = useState<string | undefined>(undefined);
  const [cost, setCost] = useState<MemoryMigrationCostResponse | null>(null);
  const [costLoading, setCostLoading] = useState(false);
  const [starting, setStarting] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const [retrying, setRetrying] = useState(false);

  const fetchMigration = useCallback(async () => {
    const m = await memoryMigrationApi.getCurrent();
    setMigration(m);
  }, []);

  // 初始加载：迁移记录 + 生效模型 + embedding 模型目录（可切换目标）。
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [migrationRes, settingsRes, modelsRes, providersRes] = await Promise.allSettled([
        memoryMigrationApi.getCurrent(),
        tenantApi.settings(),
        llmApi.listModels({ capability: 'embedding' }),
        llmApi.listProviders(),
      ]);
      if (cancelled) return;
      setLoading(false);
      if (migrationRes.status === 'fulfilled') setMigration(migrationRes.value);
      if (settingsRes.status === 'fulfilled') {
        const model = settingsRes.value.settings?.memory_embedding_model;
        setCurrentModel(typeof model === 'string' ? model : '');
      }
      if (modelsRes.status === 'fulfilled' && providersRes.status === 'fulfilled') {
        setModels(groupEmbeddingModels(modelsRes.value, providersRes.value));
        setModelsLoading(false);
      } else {
        setModelsLoading(false);
        const failed = [modelsRes, providersRes].find((r) => r.status === 'rejected');
        if (failed && failed.status === 'rejected') {
          message.error({ content: extractErrorMessage(failed.reason, '加载模型目录失败'), duration: 3 });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // migrating 期间轮询进度；状态离开 migrating 后清理。轮询失败静默续下次 tick。
  useEffect(() => {
    if (migration?.status !== 'migrating') return;
    let cancelled = false;
    let timer: number | undefined;
    const tick = async () => {
      if (cancelled) return;
      try {
        const m = await memoryMigrationApi.getCurrent();
        if (cancelled) return;
        setMigration(m);
        if (m && m.status === 'migrating') {
          timer = window.setTimeout(tick, MEMORY_MIGRATION_POLL_MS);
        }
      } catch {
        if (!cancelled) {
          timer = window.setTimeout(tick, MEMORY_MIGRATION_POLL_MS);
        }
      }
    };
    timer = window.setTimeout(tick, MEMORY_MIGRATION_POLL_MS);
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [migration?.status]);

  const fetchCost = useCallback(async (): Promise<MemoryMigrationCostResponse | null> => {
    setCostLoading(true);
    try {
      const c = await memoryMigrationApi.getCost();
      setCost(c);
      return c;
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '获取迁移成本失败'), duration: 3 });
      return null;
    } finally {
      setCostLoading(false);
    }
  }, []);

  const startMigration = useCallback(async (toModel: string) => {
    setStarting(true);
    try {
      const m = await memoryMigrationApi.start(toModel);
      setMigration(m);
      setCurrentModel(toModel);
      setTargetModel(undefined);
      setCost(null);
      message.success({ content: '迁移已启动，嵌入模型切换立即生效', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '启动迁移失败'), duration: 3 });
    } finally {
      setStarting(false);
    }
  }, []);

  const cancelMigration = useCallback(
    async (id: number) => {
      setCanceling(true);
      try {
        await memoryMigrationApi.cancel(id);
        await fetchMigration();
        message.success({ content: '迁移已取消', duration: 2 });
      } catch (err) {
        message.error({ content: extractErrorMessage(err, '取消失败'), duration: 3 });
      } finally {
        setCanceling(false);
      }
    },
    [fetchMigration],
  );

  const retryMigration = useCallback(
    async (id: number) => {
      setRetrying(true);
      try {
        await memoryMigrationApi.retry(id);
        await fetchMigration();
        message.success({ content: '已重新开始迁移', duration: 2 });
      } catch (err) {
        message.error({ content: extractErrorMessage(err, '重试失败'), duration: 3 });
      } finally {
        setRetrying(false);
      }
    },
    [fetchMigration],
  );

  return {
    migration,
    loading,
    currentModel,
    models,
    modelsLoading,
    targetModel,
    setTargetModel,
    cost,
    costLoading,
    starting,
    canceling,
    retrying,
    fetchCost,
    startMigration,
    cancelMigration,
    retryMigration,
  };
}

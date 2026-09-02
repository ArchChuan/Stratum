import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { memoryUserApi } from '../api/memory-user.api';
import type { MemoryStats } from '../model/memory';

import { extractErrorMessage } from '@/shared/lib';

export const useMyMemoriesPage = () => {
  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(true);
  const [clearLoading, setClearLoading] = useState(false);
  // 清空全部后递增，通知各 Tab 组件重新加载。
  const [reloadKey, setReloadKey] = useState(0);

  const loadStats = useCallback(async () => {
    setStatsLoading(true);
    try {
      const next = await memoryUserApi.getStats();
      setStats(next);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载记忆统计失败'), duration: 3 });
    } finally {
      setStatsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadStats();
  }, [loadStats]);

  const handleClearAll = useCallback(async () => {
    setClearLoading(true);
    try {
      await memoryUserApi.clearMyMemories();
      message.success({ content: '记忆已清空', duration: 2 });
      setReloadKey((k) => k + 1); // 各 Tab 监听 reloadKey 重新加载
      await loadStats();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '清空记忆失败'), duration: 3 });
    } finally {
      setClearLoading(false);
    }
  }, [loadStats]);

  return { stats, statsLoading, clearLoading, handleClearAll, reloadKey, reloadStats: loadStats };
};

export default useMyMemoriesPage;

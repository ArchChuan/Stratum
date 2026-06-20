import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { memoryApi } from '../api/memory.api';
import type { MemorySearchResult, MemoryStats } from '../model/memory';

import { MEMORY_SEARCH_LIMIT } from '@/constants';
import { useAuth } from '@/modules/iam';

export const useMemoryPage = () => {
  const { user } = useAuth();
  const tenantId = user?.tenant_id || '';
  const userId = user?.sub || '';

  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<MemorySearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [summary, setSummary] = useState('');
  const [sessionIdInput, setSessionIdInput] = useState('');
  const loadStats = useCallback(async () => {
    try {
      setStats(await memoryApi.stats());
    } catch {
      message.error('加载记忆统计失败');
    }
  }, []);

  const handleSearch = useCallback(
    async (query?: string) => {
      const q = (query ?? searchQuery).trim();
      if (!q) return;
      setLoading(true);
      try {
        setSearchResults(await memoryApi.search({ query: q, limit: MEMORY_SEARCH_LIMIT }));
      } catch {
        message.error('搜索记忆失败');
      } finally {
        setLoading(false);
      }
    },
    [searchQuery],
  );

  const handleDeleteMemory = useCallback(
    async (id: string) => {
      try {
        await memoryApi.delete(id);
        message.success('删除成功');
        setSearchResults((prev) => prev.filter((r) => r.entry?.id !== id));
        loadStats();
      } catch {
        message.error('删除记忆失败');
      }
    },
    [loadStats],
  );

  const loadSummary = useCallback(async () => {
    if (!sessionIdInput.trim()) {
      message.warning('请输入会话 ID');
      return;
    }
    try {
      setSummary(await memoryApi.summary(sessionIdInput, { tenant_id: tenantId, user_id: userId }));
    } catch {
      message.error('加载摘要失败');
    }
  }, [sessionIdInput, tenantId, userId]);

  useEffect(() => {
    loadStats();
  }, [loadStats]);

  return {
    searchQuery,
    setSearchQuery,
    searchResults,
    loading,
    stats,
    summary,
    sessionIdInput,
    setSessionIdInput,
    handleSearch,
    handleDeleteMemory,
    loadSummary,
  };
};

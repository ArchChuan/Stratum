import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import {
  addMemory, searchMemory, getMemoryStats,
  getMemoryEntities, getMemorySummary, deleteMemory,
} from '../services/api';
import { useAuth } from './useAuth';
import { MEMORY_SEARCH_LIMIT } from '../constants';

const useMemoryPage = () => {
  const { user } = useAuth();
  const tenantId = user?.tenant_id || '';
  const userId = user?.sub || '';

  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState([]);
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState(null);
  const [entities, setEntities] = useState([]);
  const [summary, setSummary] = useState('');
  const [sessionIdInput, setSessionIdInput] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [newMemory, setNewMemory] = useState({ role: 'user', content: '', tags: [], importance: 0.5 });

  const loadStats = useCallback(async () => {
    try {
      const res = await getMemoryStats();
      if (res.data) setStats(res.data);
    } catch {
      message.error('加载记忆统计失败');
    }
  }, []);

  const handleSearch = useCallback(async (query) => {
    const q = (query ?? searchQuery).trim();
    if (!q) return;
    setLoading(true);
    try {
      const res = await searchMemory({ query: q, limit: MEMORY_SEARCH_LIMIT });
      setSearchResults(res.data?.results || []);
    } catch {
      message.error('搜索记忆失败');
    } finally {
      setLoading(false);
    }
  }, [searchQuery]);

  const handleAddMemory = useCallback(async () => {
    if (!newMemory.content.trim()) { message.warning('请输入记忆内容'); return; }
    try {
      await addMemory({ ...newMemory, user_id: userId });
      message.success('记忆添加成功');
      setCreateOpen(false);
      setNewMemory({ role: 'user', content: '', tags: [], importance: 0.5 });
      loadStats();
      if (searchQuery.trim()) handleSearch(searchQuery);
    } catch {
      message.error('添加记忆失败');
    }
  }, [newMemory, userId, searchQuery, loadStats, handleSearch]);

  const handleDeleteMemory = useCallback(async (id) => {
    try {
      await deleteMemory(id);
      message.success('删除成功');
      setSearchResults(prev => prev.filter(r => r.entry?.id !== id));
      loadStats();
    } catch {
      message.error('删除记忆失败');
    }
  }, [loadStats]);

  const loadEntities = useCallback(async () => {
    try {
      const res = await getMemoryEntities({ tenant_id: tenantId, user_id: userId });
      setEntities(res.data?.entities || []);
    } catch {
      message.error('加载实体失败');
    }
  }, [tenantId, userId]);

  const loadSummary = useCallback(async () => {
    if (!sessionIdInput.trim()) { message.warning('请输入会话 ID'); return; }
    try {
      const res = await getMemorySummary(sessionIdInput, { tenant_id: tenantId, user_id: userId });
      setSummary(res.data?.summary || '');
    } catch {
      message.error('加载摘要失败');
    }
  }, [sessionIdInput, tenantId, userId]);

  useEffect(() => { loadStats(); }, [loadStats]);

  const resetNewMemory = useCallback(() => {
    setCreateOpen(false);
    setNewMemory({ role: 'user', content: '', tags: [], importance: 0.5 });
  }, []);

  return {
    searchQuery, setSearchQuery, searchResults, loading,
    stats, entities, summary,
    sessionIdInput, setSessionIdInput,
    createOpen, setCreateOpen, newMemory, setNewMemory,
    handleSearch, handleAddMemory, handleDeleteMemory,
    loadEntities, loadSummary, resetNewMemory,
  };
};

export default useMemoryPage;

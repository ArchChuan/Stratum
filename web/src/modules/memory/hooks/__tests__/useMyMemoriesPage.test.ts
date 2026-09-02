import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useMyMemoriesPage } from '../useMyMemoriesPage';

const getStats = vi.hoisted(() => vi.fn());
const clearMyMemories = vi.hoisted(() => vi.fn());
vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: { getStats, clearMyMemories },
}));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
vi.spyOn(message, 'success').mockImplementation(() => undefined as never);

describe('useMyMemoriesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getStats.mockResolvedValue({ memory_count: 3, entity_count: 5 });
    clearMyMemories.mockResolvedValue(undefined);
  });

  it('loads memory stats on mount', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.statsLoading).toBe(false));
    expect(getStats).toHaveBeenCalledTimes(1);
    expect(result.current.stats?.memory_count).toBe(3);
    expect(result.current.stats?.entity_count).toBe(5);
    expect(result.current.reloadKey).toBe(0);
  });

  it('reports an error when stats fail', async () => {
    // axios 响应形态：提取链取 response.data.error（FE 冻结 {error} 契约）。
    getStats.mockRejectedValue({ response: { data: { error: '加载记忆统计失败' } } });
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.statsLoading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载记忆统计失败', duration: 3 });
    expect(result.current.stats).toBeNull();
  });

  it('clears all memories, bumps reloadKey and reloads stats', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.statsLoading).toBe(false));
    expect(getStats).toHaveBeenCalledTimes(1);

    await act(async () => {
      await result.current.handleClearAll();
    });
    expect(clearMyMemories).toHaveBeenCalled();
    expect(message.success).toHaveBeenCalledWith({ content: '记忆已清空', duration: 2 });
    expect(result.current.reloadKey).toBe(1);
    expect(getStats).toHaveBeenCalledTimes(2);
  });

  it('reports an error when clearing fails', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.statsLoading).toBe(false));
    // axios 响应形态：提取链取 response.data.error（FE 冻结 {error} 契约）。
    clearMyMemories.mockRejectedValue({ response: { data: { error: '清空记忆失败' } } });

    await act(async () => {
      await result.current.handleClearAll();
    });
    expect(message.error).toHaveBeenCalledWith({ content: '清空记忆失败', duration: 3 });
    expect(result.current.reloadKey).toBe(0);
  });
});

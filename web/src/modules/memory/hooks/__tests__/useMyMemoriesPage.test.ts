import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useMyMemoriesPage } from '../useMyMemoriesPage';

const listMyMemories = vi.hoisted(() => vi.fn());
const getStats = vi.hoisted(() => vi.fn());
const deleteMemory = vi.hoisted(() => vi.fn());
const clearMyMemories = vi.hoisted(() => vi.fn());
vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: { listMyMemories, getStats, deleteMemory, clearMyMemories },
}));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
vi.spyOn(message, 'success').mockImplementation(() => undefined as never);

const fact = {
  id: 'fact-1',
  scope: 'user',
  content: 'likes Go',
  importance: 0.7,
  created_at: '2026-08-01T10:00:00Z',
  updated_at: '2026-08-01T10:00:00Z',
};

describe('useMyMemoriesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listMyMemories.mockResolvedValue({ memories: [fact], total: 1 });
    getStats.mockResolvedValue({ total_entries: 1, long_term_count: 1, short_term_count: 0, entity_count: 2 });
    deleteMemory.mockResolvedValue(undefined);
    clearMyMemories.mockResolvedValue(undefined);
  });

  it('loads the first page of memories and stats', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listMyMemories).toHaveBeenCalledWith({ page: 1, pageSize: 20 });
    expect(getStats).toHaveBeenCalled();
    expect(result.current.memories).toEqual([fact]);
    expect(result.current.total).toBe(1);
    expect(result.current.stats?.total_entries).toBe(1);
  });

  it('reports a persistent error when the list fails', async () => {
    listMyMemories.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载记忆失败', duration: 0 });
    expect(result.current.memories).toEqual([]);
  });

  it('reports an error when stats fail', async () => {
    getStats.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.statsLoading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载记忆统计失败', duration: 0 });
  });

  it('refetches the current page on page change', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    listMyMemories.mockResolvedValue({ memories: [], total: 2 });

    await act(async () => {
      await result.current.handlePageChange(2, 10);
    });
    expect(listMyMemories).toHaveBeenLastCalledWith({ page: 2, pageSize: 10 });
  });

  it('deletes a memory and reloads list and stats', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.handleDelete('fact-1');
    });
    expect(deleteMemory).toHaveBeenCalledWith('fact-1');
    expect(message.success).toHaveBeenCalledWith({ content: '记忆已删除', duration: 2 });
    expect(listMyMemories).toHaveBeenLastCalledWith({ page: 1, pageSize: 20 });
  });

  it('reports a persistent error when deletion fails', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    deleteMemory.mockRejectedValue(new Error('failed'));

    await act(async () => {
      await result.current.handleDelete('fact-1');
    });
    expect(message.error).toHaveBeenCalledWith({ content: '删除记忆失败', duration: 0 });
  });

  it('clears all memories and resets to page 1', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.handleClearAll();
    });
    expect(clearMyMemories).toHaveBeenCalled();
    expect(message.success).toHaveBeenCalledWith({ content: '已清空全部记忆', duration: 2 });
    expect(listMyMemories).toHaveBeenLastCalledWith({ page: 1, pageSize: 20 });
  });
});

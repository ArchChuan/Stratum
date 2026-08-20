import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useMyMemoriesPage } from '../useMyMemoriesPage';

const listMyMemories = vi.hoisted(() => vi.fn());
const getStats = vi.hoisted(() => vi.fn());
const listMyEntities = vi.hoisted(() => vi.fn());
const clearMyMemories = vi.hoisted(() => vi.fn());
vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: { listMyMemories, getStats, listMyEntities, clearMyMemories },
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

const entity = {
  id: 'ent-1',
  name: 'Go',
  entity_type: 'tech',
  fact_count: 4,
  last_seen_at: '2026-08-01T10:00:00Z',
};

describe('useMyMemoriesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listMyMemories.mockResolvedValue({ memories: [fact], total: 1 });
    getStats.mockResolvedValue({ memory_count: 1, entity_count: 2 });
    listMyEntities.mockResolvedValue({ entities: [entity], total: 1 });
    clearMyMemories.mockResolvedValue(undefined);
  });

  it('loads the first page of memories, stats and entities', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listMyMemories).toHaveBeenCalledWith({ page: 1, pageSize: 20 });
    expect(getStats).toHaveBeenCalled();
    expect(listMyEntities).toHaveBeenCalledWith({ page: 1, pageSize: 20 });
    expect(result.current.memories).toEqual([fact]);
    expect(result.current.total).toBe(1);
    expect(result.current.stats?.memory_count).toBe(1);
    expect(result.current.stats?.entity_count).toBe(2);
    expect(result.current.entities).toEqual([entity]);
    expect(result.current.entityTotal).toBe(1);
  });

  it('reports a persistent error when the list fails', async () => {
    listMyMemories.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载记忆失败', duration: 3 });
    expect(result.current.memories).toEqual([]);
  });

  it('reports an error when stats fail', async () => {
    getStats.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.statsLoading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载记忆统计失败', duration: 3 });
  });

  it('reports an error when entities fail', async () => {
    listMyEntities.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.entitiesLoading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载记忆实体失败', duration: 3 });
    expect(result.current.entities).toEqual([]);
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

  it('refetches entities on entity page change', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.entitiesLoading).toBe(false));
    listMyEntities.mockResolvedValue({ entities: [], total: 5 });

    await act(async () => {
      await result.current.handleEntityPageChange(2, 10);
    });
    expect(listMyEntities).toHaveBeenLastCalledWith({ page: 2, pageSize: 10 });
    expect(result.current.entityPage).toBe(2);
    expect(result.current.entityTotal).toBe(5);
  });

  it('discards stale entity responses when pages race', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.entitiesLoading).toBe(false));

    let resolvePage2: (v: unknown) => void = () => undefined;
    listMyEntities.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePage2 = resolve;
        }),
    );
    await act(async () => {
      void result.current.handleEntityPageChange(2, 10);
    });
    listMyEntities.mockResolvedValue({ entities: [entity], total: 1 });
    await act(async () => {
      await result.current.handleEntityPageChange(3, 10);
    });
    // 旧响应（page 2）迟到，不得覆盖 page 3 数据。
    await act(async () => {
      resolvePage2({ entities: [], total: 99 });
    });

    expect(result.current.entityTotal).toBe(1);
    expect(result.current.entities).toEqual([entity]);
  });

  it('clears all memories and reloads list, stats and entities', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.handleClearAll();
    });
    expect(clearMyMemories).toHaveBeenCalled();
    expect(message.success).toHaveBeenCalledWith({ content: '已清空全部记忆', duration: 2 });
    expect(listMyMemories).toHaveBeenLastCalledWith({ page: 1, pageSize: 20 });
    expect(listMyEntities).toHaveBeenLastCalledWith({ page: 1, pageSize: 20 });
  });

  it('reports an error when clearing fails', async () => {
    const { result } = renderHook(() => useMyMemoriesPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    clearMyMemories.mockRejectedValue(new Error('failed'));

    await act(async () => {
      await result.current.handleClearAll();
    });
    expect(message.error).toHaveBeenCalledWith({ content: '清空记忆失败', duration: 3 });
  });
});

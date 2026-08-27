import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useFactsTab } from '../useFactsTab';

const { listFactsMock, deleteFactMock } = vi.hoisted(() => ({
  listFactsMock: vi.fn(),
  deleteFactMock: vi.fn(),
}));

vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: {
    listFacts: listFactsMock,
    deleteFact: deleteFactMock,
    updateFact: vi.fn(),
  },
}));

describe('useFactsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    listFactsMock.mockResolvedValue({
      facts: [
        {
          id: 'fact-1',
          scope: 'user',
          content: 'dark mode',
          importance: 0.9,
          created_at: '2026-08-01T10:00:00Z',
          updated_at: '2026-08-01T10:00:00Z',
          confidence: 0.9,
          category: 'preference',
          source: 'conversation',
          status: 'active',
        },
      ],
      total: 1,
    });
  });

  it('loads facts on mount', async () => {
    const { result } = renderHook(() => useFactsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listFactsMock).toHaveBeenCalled();
    expect(result.current.facts).toHaveLength(1);
  });

  it('deletes a fact and reloads', async () => {
    deleteFactMock.mockResolvedValue(undefined);
    const { result } = renderHook(() => useFactsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.deleteFact('fact-1');
    });
    expect(deleteFactMock).toHaveBeenCalledWith('fact-1');
    expect(message.success).toHaveBeenCalled();
    expect(listFactsMock).toHaveBeenCalledTimes(2);
  });

  it('resets to page 1 when filters change', async () => {
    const { result } = renderHook(() => useFactsTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listFactsMock.mock.calls[0][0].page).toBe(1);

    // 翻到第 3 页
    await act(async () => {
      result.current.pagination.onChange(3, result.current.pagination.pageSize);
    });
    await waitFor(() => expect(listFactsMock).toHaveBeenCalledTimes(2));
    expect(listFactsMock.mock.calls[1][0].page).toBe(3);

    // 筛选变化 → 页码重置回第 1 页
    await act(async () => {
      result.current.applyFilters({ q: 'dark' });
    });
    await waitFor(() => expect(listFactsMock).toHaveBeenCalledTimes(3));
    expect(listFactsMock.mock.calls[2][0]).toMatchObject({ page: 1, q: 'dark' });
  });
});

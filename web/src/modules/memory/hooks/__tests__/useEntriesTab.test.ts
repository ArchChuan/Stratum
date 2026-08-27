import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useEntriesTab } from '../useEntriesTab';

const { listEntriesMock, deleteEntryMock } = vi.hoisted(() => ({
  listEntriesMock: vi.fn(),
  deleteEntryMock: vi.fn(),
}));

vi.mock('../../api/memory-user.api', () => ({
  memoryUserApi: {
    listEntries: listEntriesMock,
    deleteEntry: deleteEntryMock,
  },
}));

describe('useEntriesTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    listEntriesMock.mockResolvedValue({
      entries: [
        {
          id: 'e-1',
          role: 'user',
          content: 'hello',
          type: 'message',
          scope: 'user',
          importance: 0.5,
          created_at: '2026-08-01T10:00:00Z',
          expires_at: null,
        },
      ],
      total: 1,
    });
  });

  it('loads entries and applies query filter', async () => {
    const { result } = renderHook(() => useEntriesTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.entries).toHaveLength(1);

    await act(async () => {
      result.current.setQuery('hello');
    });
    await waitFor(() => expect(listEntriesMock).toHaveBeenCalledWith(expect.objectContaining({ q: 'hello' })));
  });

  it('deletes an entry', async () => {
    deleteEntryMock.mockResolvedValue(undefined);
    const { result } = renderHook(() => useEntriesTab());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.deleteEntry('e-1');
    });
    expect(deleteEntryMock).toHaveBeenCalledWith('e-1');
  });
});

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { memoryUserApi } from './memory-user.api';

import api from '@/services/client';

vi.mock('@/services/client', () => ({
  default: { delete: vi.fn(), get: vi.fn() },
}));

describe('memoryUserApi', () => {
  beforeEach(() => {
    vi.mocked(api.delete).mockReset();
    vi.mocked(api.delete).mockResolvedValue({} as never);
    vi.mocked(api.get).mockReset();
  });

  it('clears memories through the backend-relative route', async () => {
    await memoryUserApi.clearMyMemories();

    expect(api.delete).toHaveBeenCalledWith('/memory/clear');
  });

  it('lists my memories with pagination params', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        memories: [
          {
            id: 'fact-1',
            scope: 'user',
            content: 'likes Go',
            importance: 0.7,
            created_at: '2026-08-01T10:00:00Z',
            updated_at: '2026-08-01T10:00:00Z',
          },
        ],
        total: 1,
      },
    } as never);

    const pageData = await memoryUserApi.listMyMemories({ page: 2, pageSize: 10 });

    expect(api.get).toHaveBeenCalledWith('/memory', {
      params: { page: 2, page_size: 10 },
    });
    expect(pageData.total).toBe(1);
    expect(pageData.memories[0].id).toBe('fact-1');
    expect(pageData.memories[0].created_at).toBe('2026-08-01T10:00:00Z');
  });

  it('rejects a list payload missing total', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: { memories: [] } } as never);

    await expect(memoryUserApi.listMyMemories({ page: 1, pageSize: 20 })).rejects.toThrow();
  });

  it('fetches memory stats', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { total_entries: 5, long_term_count: 2, short_term_count: 3, entity_count: 1 },
    } as never);

    const stats = await memoryUserApi.getStats();

    expect(api.get).toHaveBeenCalledWith('/memory/stats');
    expect(stats.total_entries).toBe(5);
    expect(stats.long_term_count).toBe(2);
  });

  it('deletes a single memory by id', async () => {
    await memoryUserApi.deleteMemory('fact/1');

    expect(api.delete).toHaveBeenCalledWith('/memory/fact%2F1');
  });
});

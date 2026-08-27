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
            confidence: 0.8,
            category: 'preference',
            source: 'conversation',
            status: 'active',
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

  it('fetches user-level memory stats', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: { memory_count: 5, entity_count: 3, embed_model_configured: true },
    } as never);

    const stats = await memoryUserApi.getStats();

    expect(api.get).toHaveBeenCalledWith('/memory/stats');
    expect(stats.memory_count).toBe(5);
    expect(stats.entity_count).toBe(3);
    expect(stats.embed_model_configured).toBe(true);
  });

  it('defaults missing stats fields to zero', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: {} } as never);

    const stats = await memoryUserApi.getStats();

    expect(stats.memory_count).toBe(0);
    expect(stats.entity_count).toBe(0);
  });

  it('lists my entity topic tags with pagination params', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        entities: [
          {
            id: 'ent-1',
            name: 'Go',
            entity_type: 'tech',
            fact_count: 4,
            last_seen_at: '2026-08-01T10:00:00Z',
          },
        ],
        total: 1,
      },
    } as never);

    const pageData = await memoryUserApi.listMyEntities({ page: 2, pageSize: 10 });

    expect(api.get).toHaveBeenCalledWith('/memory/entities', {
      params: { page: 2, page_size: 10 },
    });
    expect(pageData.total).toBe(1);
    expect(pageData.entities[0].name).toBe('Go');
    expect(pageData.entities[0].fact_count).toBe(4);
  });
});

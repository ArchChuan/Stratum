import { beforeEach, describe, expect, it, vi } from 'vitest';

import { memoryMigrationApi } from './memory-migration.api';

import api from '@/services/client';

vi.mock('@/services/client', () => ({
  default: { get: vi.fn(), post: vi.fn() },
}));

const migrationRecord = {
  id: 7,
  from_model: 'text-embedding-v1',
  to_model: 'text-embedding-v3',
  status: 'migrating',
  progress: 10,
  total_facts: 100,
  created_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:00:00Z',
};

describe('memoryMigrationApi', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.post).mockReset();
  });

  it('fetches current migration through the tenant-scoped route', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: migrationRecord } as never);

    const m = await memoryMigrationApi.getCurrent();

    expect(api.get).toHaveBeenCalledWith('/tenant/memory/migrations/current');
    expect(m?.id).toBe(7);
    expect(m?.status).toBe('migrating');
  });

  it('returns null when the tenant has never migrated', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: null } as never);

    const m = await memoryMigrationApi.getCurrent();

    expect(m).toBeNull();
  });

  it('fetches migration cost preview', async () => {
    vi.mocked(api.get).mockResolvedValue({ data: { fact_count: 120, estimated_seconds: 24 } } as never);

    const cost = await memoryMigrationApi.getCost();

    expect(api.get).toHaveBeenCalledWith('/tenant/memory/migrations/cost');
    expect(cost.fact_count).toBe(120);
    expect(cost.estimated_seconds).toBe(24);
  });

  it('starts a migration with the target model', async () => {
    vi.mocked(api.post).mockResolvedValue({ data: migrationRecord } as never);

    const m = await memoryMigrationApi.start('text-embedding-v3');

    expect(api.post).toHaveBeenCalledWith('/tenant/memory/migrations', {
      to_model: 'text-embedding-v3',
    });
    expect(m.to_model).toBe('text-embedding-v3');
  });

  it('cancels a migration by id', async () => {
    vi.mocked(api.post).mockResolvedValue({} as never);

    await memoryMigrationApi.cancel(7);

    expect(api.post).toHaveBeenCalledWith('/tenant/memory/migrations/7/cancel');
  });

  it('retries a migration by id', async () => {
    vi.mocked(api.post).mockResolvedValue({} as never);

    await memoryMigrationApi.retry(9);

    expect(api.post).toHaveBeenCalledWith('/tenant/memory/migrations/9/retry');
  });
});

import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useMemoryMigration } from '../useMemoryMigration';

import { MEMORY_MIGRATION_POLL_MS } from '@/constants';

const getCurrent = vi.hoisted(() => vi.fn());
const getCost = vi.hoisted(() => vi.fn());
const start = vi.hoisted(() => vi.fn());
const cancel = vi.hoisted(() => vi.fn());
const retry = vi.hoisted(() => vi.fn());
vi.mock('../../api/memory-migration.api', () => ({
  memoryMigrationApi: { getCurrent, getCost, start, cancel, retry },
}));

const listModels = vi.hoisted(() => vi.fn());
const listProviders = vi.hoisted(() => vi.fn());
vi.mock('@/modules/llm', () => ({ llmApi: { listModels, listProviders } }));

const settings = vi.hoisted(() => vi.fn());
// 从 __tests__/ 到 iam/api/tenant.api 需要两级回退（hooks/__tests__ → hooks → iam/api）。
vi.mock('../../api/tenant.api', () => ({ tenantApi: { settings } }));

vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
vi.spyOn(message, 'success').mockImplementation(() => undefined as never);

const migratingRecord = {
  id: 7,
  from_model: 'text-embedding-v1',
  to_model: 'text-embedding-v3',
  status: 'migrating',
  progress: 10,
  total_facts: 100,
  created_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:00:00Z',
};
const doneRecord = { ...migratingRecord, status: 'done', progress: 100 };

const model = {
  id: 'm1',
  providerId: 'p1',
  name: 'text-embedding-v3',
  displayName: 'v3',
  capabilities: ['embedding'],
  enabled: true,
};
const provider = { id: 'p1', name: 'OpenAI' };

describe('useMemoryMigration', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getCurrent.mockResolvedValue(null);
    getCost.mockResolvedValue({ fact_count: 120, estimated_seconds: 24 });
    start.mockResolvedValue(migratingRecord);
    cancel.mockResolvedValue(undefined);
    retry.mockResolvedValue(undefined);
    settings.mockResolvedValue({ settings: { memory_embedding_model: 'text-embedding-v1' } });
    listModels.mockResolvedValue([model]);
    listProviders.mockResolvedValue([provider]);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('loads migration, current model and grouped embedding catalog on mount', async () => {
    getCurrent.mockResolvedValue(null);
    const { result } = renderHook(() => useMemoryMigration());

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.currentModel).toBe('text-embedding-v1');
    expect(result.current.migration).toBeNull();
    expect(result.current.models).toEqual([
      { provider: 'OpenAI', models: [{ value: 'text-embedding-v3', label: 'v3', health: undefined }] },
    ]);
  });

  it('keeps current model empty when tenant has no memory_embedding_model', async () => {
    settings.mockResolvedValue({ settings: {} });
    const { result } = renderHook(() => useMemoryMigration());

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.currentModel).toBe('');
  });

  it('starts a migration and immediately reflects the new effective model', async () => {
    const { result } = renderHook(() => useMemoryMigration());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.startMigration('text-embedding-v3');
    });

    expect(start).toHaveBeenCalledWith('text-embedding-v3');
    expect(result.current.migration?.status).toBe('migrating');
    expect(result.current.currentModel).toBe('text-embedding-v3');
  });

  it('cancels and refreshes the migration record', async () => {
    getCurrent.mockResolvedValue({ ...migratingRecord, status: 'canceled' });
    const { result } = renderHook(() => useMemoryMigration());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.cancelMigration(7);
    });

    expect(cancel).toHaveBeenCalledWith(7);
    expect(result.current.migration?.status).toBe('canceled');
  });

  it('retries a failed migration and refreshes the record', async () => {
    getCurrent.mockResolvedValue({ ...migratingRecord, status: 'failed' });
    const { result } = renderHook(() => useMemoryMigration());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.retryMigration(7);
    });

    expect(retry).toHaveBeenCalledWith(7);
    expect(result.current.migration?.status).toBe('failed');
  });

  it('fetches cost preview', async () => {
    const { result } = renderHook(() => useMemoryMigration());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.fetchCost();
    });

    expect(getCost).toHaveBeenCalled();
    expect(result.current.cost?.fact_count).toBe(120);
  });

  it('polls progress while migrating and stops once done', async () => {
    vi.useFakeTimers();
    getCurrent
      .mockResolvedValueOnce(migratingRecord) // 初始加载即 migrating
      .mockResolvedValueOnce(doneRecord); // 首次轮询即 done

    const { result } = renderHook(() => useMemoryMigration());
    await act(async () => {
      // 冲刷初始加载（getCurrent#1 → migration=migrating → 轮询 effect 已排期）
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(getCurrent).toHaveBeenCalledTimes(1);
    expect(result.current.migration?.status).toBe('migrating');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(MEMORY_MIGRATION_POLL_MS);
    });

    // 轮询触发 getCurrent#2 → done，停止轮询
    expect(getCurrent).toHaveBeenCalledTimes(2);
    expect(result.current.migration?.status).toBe('done');
  });
});

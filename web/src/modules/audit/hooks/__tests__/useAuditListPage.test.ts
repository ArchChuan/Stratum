import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useAuditListPage } from '../useAuditListPage';

const listEvents = vi.hoisted(() => vi.fn());
const getEvent = vi.hoisted(() => vi.fn());
vi.mock('../../api/audit.api', () => ({ auditApi: { listEvents, getEvent } }));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);

const event = {
  id: 'evt-1',
  resource_kind: 'workflow',
  resource_id: 'wf-1',
  operation: 'create',
  actor_id: 'admin-1',
  actor_name: '管理员甲',
  created_at: '2026-08-09T11:00:00Z',
  before: null,
  after: { status: 'draft' },
};

describe('useAuditListPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listEvents.mockResolvedValue({ events: [event], total: 1 });
    getEvent.mockResolvedValue(event);
  });

  it('loads the first page of audit events', async () => {
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(listEvents).toHaveBeenCalledWith({ page: 1, pageSize: 20 });
    expect(result.current.events).toEqual([event]);
    expect(result.current.total).toBe(1);
  });

  it('reports a persistent error when the list fails', async () => {
    listEvents.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(message.error).toHaveBeenCalledWith({ content: '加载审计记录失败', duration: 0 });
    expect(result.current.events).toEqual([]);
  });

  it('applies filters and resets to page 1', async () => {
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    listEvents.mockResolvedValue({ events: [], total: 0 });

    await act(async () => {
      result.current.applyFilters({ resourceKind: 'workflow', actorName: '管理员甲' });
    });
    expect(listEvents).toHaveBeenLastCalledWith({
      page: 1,
      pageSize: 20,
      resourceKind: 'workflow',
      actorName: '管理员甲',
    });
    expect(result.current.filters).toEqual({ resourceKind: 'workflow', actorName: '管理员甲' });
  });

  it('refetches with the active filters on page change', async () => {
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    listEvents.mockResolvedValue({ events: [], total: 2 });

    await act(async () => {
      result.current.applyFilters({ resourceKind: 'workflow' });
    });
    listEvents.mockResolvedValue({ events: [], total: 2 });

    await act(async () => {
      await result.current.handlePageChange(2, 10);
    });
    expect(listEvents).toHaveBeenLastCalledWith({ page: 2, pageSize: 10, resourceKind: 'workflow' });
  });

  it('opens a detail and loads the full event', async () => {
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));

    await act(async () => {
      await result.current.openDetail('evt-1');
    });
    expect(getEvent).toHaveBeenCalledWith('evt-1');
    expect(result.current.detailEvent).toEqual(event);
    expect(result.current.detailLoading).toBe(false);
  });

  it('closes the detail and clears the loaded event', async () => {
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await act(async () => {
      await result.current.openDetail('evt-1');
    });
    act(() => result.current.closeDetail());
    expect(result.current.detailId).toBeNull();
    expect(result.current.detailEvent).toBeNull();
  });
});

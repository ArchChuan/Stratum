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
  tenant_id: 't1',
  actor: { actor_type: 'user', actor_id: 'admin-1' },
  action: 'POST /v1/agents',
  resource_type: 'http_request',
  resource_id: 'agent-1',
  request_id: 'req-1',
  trace_id: 'trace-1',
  risk_level: 'medium',
  outcome: 'success',
  occurred_at: '2026-08-09T11:00:00Z',
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
      result.current.applyFilters({ risk_level: 'high', outcome: 'error' });
    });
    expect(listEvents).toHaveBeenLastCalledWith({
      page: 1,
      pageSize: 20,
      risk_level: 'high',
      outcome: 'error',
    });
    expect(result.current.filters).toEqual({ risk_level: 'high', outcome: 'error' });
  });

  it('refetches with the active filters on page change', async () => {
    const { result } = renderHook(() => useAuditListPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    listEvents.mockResolvedValue({ events: [], total: 2 });

    await act(async () => {
      result.current.applyFilters({ risk_level: 'high' });
    });
    listEvents.mockResolvedValue({ events: [], total: 2 });

    await act(async () => {
      await result.current.handlePageChange(2, 10);
    });
    expect(listEvents).toHaveBeenLastCalledWith({ page: 2, pageSize: 10, risk_level: 'high' });
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

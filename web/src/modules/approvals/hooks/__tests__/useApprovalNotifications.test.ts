import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useApprovalNotifications } from '../useApprovalNotifications';

const listPending = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
  approvalApi: { listPending },
}));

const pendingRow = {
  id: 'ap-1',
  subject_kind: 'mcp_tool',
  tool_name: 'read_file',
  server_id: 'srv-1',
  risk_level: 'read',
  status: 'pending',
  user_id: 'user-1',
  created_at: '2026-08-13T10:00:00Z',
  expires_at: '2026-08-13T11:00:00Z',
};

describe('useApprovalNotifications', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPending.mockResolvedValue([pendingRow]);
  });

  it('loads pending rows on mount for the badge count', async () => {
    const { result } = renderHook(() => useApprovalNotifications());
    await waitFor(() => expect(result.current.rows).toEqual([pendingRow]));
    expect(listPending).toHaveBeenCalledTimes(1);
    expect(result.current.loading).toBe(false);
  });

  it('refreshes immediately when the window regains focus', async () => {
    const { result } = renderHook(() => useApprovalNotifications());
    await waitFor(() => expect(result.current.rows).toEqual([pendingRow]));

    listPending.mockResolvedValue([]);
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });
    await waitFor(() => expect(result.current.rows).toEqual([]));
    expect(listPending).toHaveBeenCalledTimes(2);
  });

  it('drops a stale in-flight response so old data never overwrites newer state', async () => {
    let resolveFirst: (rows: typeof pendingRow[]) => void = () => undefined;
    listPending.mockReturnValueOnce(new Promise((r) => { resolveFirst = r; }));
    listPending.mockResolvedValueOnce([]);

    const { result } = renderHook(() => useApprovalNotifications());
    await waitFor(() => expect(listPending).toHaveBeenCalledTimes(1));

    // 窗口聚焦触发第二次刷新（先返回新数据 []）；旧的挂起响应随后到达，必须被
    // refresh 内自增 seq 丢弃，避免用过期数据覆盖新数据（轮询竞态）。
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });
    await waitFor(() => expect(result.current.rows).toEqual([]));

    await act(async () => {
      resolveFirst([pendingRow]);
    });
    expect(result.current.rows).toEqual([]);
  });

  it('silently ignores list failures so the badge never spams errors', async () => {
    listPending.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useApprovalNotifications());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.rows).toEqual([]);
  });
});

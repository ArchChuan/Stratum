import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useApprovalsPage } from '../useApprovalsPage';

const listPending = vi.hoisted(() => vi.fn());
const listHistory = vi.hoisted(() => vi.fn());
const getDetail = vi.hoisted(() => vi.fn());
const execute = vi.hoisted(() => vi.fn());
const setAssignee = vi.hoisted(() => vi.fn());
const decide = vi.hoisted(() => vi.fn());
const members = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
  approvalApi: { listPending, listHistory, getDetail, execute, setAssignee, decide },
}));
vi.mock('@/modules/iam', () => ({
  tenantApi: { members },
  Member: {},
}));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
vi.spyOn(message, 'success').mockImplementation(() => undefined as never);

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

const historyRow = { ...pendingRow, id: 'ap-2', status: 'approved', decided_by: 'admin-1' };

const pendingPromise = async () => [pendingRow];

describe('useApprovalsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPending.mockResolvedValue(pendingPromise());
    listHistory.mockResolvedValue({ approvals: [historyRow], total: 1, page: 1, page_size: 20 });
    getDetail.mockResolvedValue({ ...pendingRow, payload: {} });
    execute.mockResolvedValue({ status: 'executed' });
    setAssignee.mockResolvedValue(undefined);
    decide.mockResolvedValue(undefined);
  });

  it('loads the pending list on mount', async () => {
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));
    expect(listPending).toHaveBeenCalled();
    expect(result.current.pendingRows).toEqual([pendingRow]);
  });

  it('switches to history tab and loads the first page', async () => {
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      result.current.switchTab('history');
    });
    await waitFor(() => expect(result.current.historyLoading).toBe(false));
    expect(listHistory).toHaveBeenCalledWith(1, 20);
    expect(result.current.historyRows).toEqual([historyRow]);
    expect(result.current.total).toBe(1);
  });

  it('requests admin/owner members as assignable approvers (backend role filter)', async () => {
    members.mockResolvedValue({
      members: [
        { user_id: 'u-admin', role: 'admin', github_login: 'admin-user' },
        { user_id: 'u-owner', role: 'owner', github_login: 'owner-user' },
      ],
      total: 2,
    });
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      await result.current.loadApprovers();
    });
    expect(members).toHaveBeenCalledWith(1, 100, 'admin,owner');
    expect(result.current.approvers.map((m) => m.user_id)).toEqual(['u-admin', 'u-owner']);
  });

  it('does not let a history load drop the in-flight pending list or its loading flag', async () => {
    // pending 慢响应 + 立即切历史：pending 列表必须保留自己的 loading 复位，不被
    // history 的 seq 失效；history 慢响应后 pending 也已加载完成。
    let resolvePending: (rows: typeof pendingRow[]) => void = () => undefined;
    listPending.mockReturnValue(new Promise((r) => { resolvePending = r; }));

    const { result } = renderHook(() => useApprovalsPage());
    // pending 在途：loading 保持 true。
    expect(result.current.pendingLoading).toBe(true);

    await act(async () => {
      result.current.switchTab('history');
    });
    await waitFor(() => expect(result.current.historyLoading).toBe(false));

    await act(async () => {
      resolvePending([pendingRow]);
    });
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));
    expect(result.current.pendingRows).toEqual([pendingRow]);
  });

  it('decides and refreshes the pending list on success', async () => {
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));
    listPending.mockResolvedValue([]);

    let ok = false;
    await act(async () => {
      ok = await result.current.decide('ap-1', 'approved', 'ok');
    });
    expect(decide).toHaveBeenCalledWith('ap-1', 'approved', 'ok');
    expect(ok).toBe(true);
    expect(message.success).toHaveBeenCalled();
    await waitFor(() => expect(result.current.pendingRows).toEqual([]));
  });

  it('reports a persistent error and returns false when decide fails', async () => {
    decide.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    let ok = true;
    await act(async () => {
      ok = await result.current.decide('ap-1', 'rejected');
    });
    expect(ok).toBe(false);
    expect(message.error).toHaveBeenCalledWith({ content: '操作失败', duration: 0 });
  });

  it('assigns an approver via the tenant admin surface', async () => {
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    let ok = false;
    await act(async () => {
      ok = await result.current.assign('ap-1', 'u-admin');
    });
    expect(setAssignee).toHaveBeenCalledWith('ap-1', 'u-admin');
    expect(ok).toBe(true);
    expect(message.success).toHaveBeenCalledWith({ content: '指派成功', duration: 2 });
  });

  it('opens a detail and loads the full payload view', async () => {
    const { result } = renderHook(() => useApprovalsPage());
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      await result.current.openDetail('ap-1');
    });
    expect(getDetail).toHaveBeenCalledWith('ap-1');
    expect(result.current.detail?.id).toBe('ap-1');
  });
});

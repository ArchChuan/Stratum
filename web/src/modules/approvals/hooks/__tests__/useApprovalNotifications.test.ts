import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useApprovalNotifications } from '../useApprovalNotifications';

const listPending = vi.hoisted(() => vi.fn());
const opListPending = vi.hoisted(() => vi.fn());
const opListMine = vi.hoisted(() => vi.fn());
const isAdmin = vi.hoisted(() => vi.fn());

vi.mock('../../api', () => ({
  approvalApi: { listPending },
}));
vi.mock('@/modules/iam', () => ({
  useTenantRole: () => ({ isAdmin: isAdmin() }),
}));
vi.mock('@/modules/operation-gate/api/operationProposal.api', () => ({
  operationProposalApi: { listPending: opListPending, listMine: opListMine },
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

const proposalRow = {
  id: 'op-1',
  agentId: 'agent-1',
  opType: 'grant_editor',
  payloadSummary: { resourceName: 'workspace/文档A' },
  status: 'proposed',
  proposerId: 'user-1',
  createdAt: '2026-08-26T09:00:00Z',
};
const doneProposal = { ...proposalRow, id: 'op-2', status: 'approved' };

describe('useApprovalNotifications', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPending.mockResolvedValue([pendingRow]);
    opListPending.mockResolvedValue([]);
    opListMine.mockResolvedValue([]);
  });

  it('admin badge merges tool approvals with all pending proposals', async () => {
    isAdmin.mockReturnValue(true);
    opListPending.mockResolvedValue([proposalRow]);
    const { result } = renderHook(() => useApprovalNotifications());

    await waitFor(() => expect(result.current.items.length).toBe(2));
    expect(opListMine).not.toHaveBeenCalled();
    expect(result.current.items[0]).toMatchObject({ key: 'tool:ap-1', tab: 'tools' });
    expect(result.current.items[1]).toMatchObject({ key: 'proposal:op-1', tab: 'permission' });
    expect(result.current.loading).toBe(false);
  });

  it('member only pulls own proposals and filters non-pending states', async () => {
    isAdmin.mockReturnValue(false);
    opListMine.mockResolvedValue([proposalRow, doneProposal]);
    const { result } = renderHook(() => useApprovalNotifications());

    await waitFor(() => expect(result.current.items.length).toBe(2));
    expect(opListPending).not.toHaveBeenCalled();
    // doneProposal（approved）是终态，不计入铃铛角标。
    expect(result.current.items.map((i) => i.key)).toEqual(['tool:ap-1', 'proposal:op-1']);
  });

  it('refreshes immediately when the window regains focus', async () => {
    isAdmin.mockReturnValue(true);
    const { result } = renderHook(() => useApprovalNotifications());
    await waitFor(() => expect(result.current.items.length).toBe(1));

    listPending.mockResolvedValue([]);
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });
    await waitFor(() => expect(result.current.items).toEqual([]));
    expect(listPending).toHaveBeenCalledTimes(2);
  });

  it('drops a stale in-flight response so old data never overwrites newer state', async () => {
    isAdmin.mockReturnValue(true);
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
    await waitFor(() => expect(result.current.items.length).toBe(0));

    await act(async () => {
      resolveFirst([pendingRow]);
    });
    expect(result.current.items.length).toBe(0);
  });

  it('silently ignores list failures so the badge never spams errors', async () => {
    isAdmin.mockReturnValue(true);
    listPending.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useApprovalNotifications());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items).toEqual([]);
  });
});

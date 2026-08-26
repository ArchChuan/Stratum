import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useOperationProposals } from '../useOperationProposals';

import type { OperationProposal } from '../../model/operationProposal';

const listPending = vi.hoisted(() => vi.fn());
const listMine = vi.hoisted(() => vi.fn());
const listHistory = vi.hoisted(() => vi.fn());
const get = vi.hoisted(() => vi.fn());
const startReview = vi.hoisted(() => vi.fn());
const approve = vi.hoisted(() => vi.fn());
const reject = vi.hoisted(() => vi.fn());
const cancel = vi.hoisted(() => vi.fn());

vi.mock('../../api/operationProposal.api', () => ({
  operationProposalApi: { listPending, listMine, listHistory, get, startReview, approve, reject, cancel },
}));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
vi.spyOn(message, 'success').mockImplementation(() => undefined as never);

const pendingProposal: OperationProposal = {
  id: 'op-1',
  agentId: 'agent-1',
  opType: 'self_modify',
  payloadSummary: {},
  status: 'proposed',
  proposerId: 'user-1',
  createdAt: '2026-08-26T09:00:00Z',
};
const reviewingProposal: OperationProposal = { ...pendingProposal, id: 'op-2', status: 'reviewing' };
const approvedProposal: OperationProposal = { ...pendingProposal, id: 'op-3', status: 'approved' };
const executedProposal: OperationProposal = { ...pendingProposal, id: 'op-4', status: 'executed' };

describe('useOperationProposals', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listPending.mockResolvedValue([pendingProposal, reviewingProposal]);
    listMine.mockResolvedValue([pendingProposal, approvedProposal]);
    listHistory.mockResolvedValue({ proposals: [executedProposal], total: 1, page: 1, pageSize: 20 });
    cancel.mockResolvedValue(undefined);
  });

  it('admin loads all pending proposals on mount', async () => {
    const { result } = renderHook(() => useOperationProposals(false));
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));
    expect(listPending).toHaveBeenCalledTimes(1);
    expect(listMine).not.toHaveBeenCalled();
    expect(result.current.pending).toEqual([pendingProposal, reviewingProposal]);
  });

  it('member loads own proposals and filters to pending states', async () => {
    const { result } = renderHook(() => useOperationProposals(true));
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));
    expect(listMine).toHaveBeenCalledTimes(1);
    expect(listPending).not.toHaveBeenCalled();
    // approvedProposal 是终态，不属于待审批子 tab。
    expect(result.current.pending).toEqual([pendingProposal]);
  });

  it('switching to history loads the first page from scratch', async () => {
    const { result } = renderHook(() => useOperationProposals(false));
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      result.current.switchTab('history');
    });
    await waitFor(() => expect(result.current.historyLoading).toBe(false));
    expect(listHistory).toHaveBeenCalledWith(1, 20);
    expect(result.current.history).toEqual([executedProposal]);
    expect(result.current.total).toBe(1);
  });

  it('history page change reloads with the next page and size', async () => {
    listHistory.mockResolvedValue({ proposals: [], total: 0, page: 2, pageSize: 20 });
    const { result } = renderHook(() => useOperationProposals(false));
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      result.current.switchTab('history');
    });
    await waitFor(() => expect(result.current.historyLoading).toBe(false));

    await act(async () => {
      result.current.handleHistoryPageChange(2, 20);
    });
    await waitFor(() => expect(result.current.historyLoading).toBe(false));
    expect(listHistory).toHaveBeenLastCalledWith(2, 20);
  });

  it('member cancels own pending proposal then refreshes and closes the drawer', async () => {
    const { result } = renderHook(() => useOperationProposals(true));
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      void result.current.openDetail(pendingProposal);
    });
    expect(result.current.detailOpen).toBe(true);

    listMine.mockResolvedValue([]);
    await act(async () => {
      await result.current.handleCancel();
    });
    expect(cancel).toHaveBeenCalledWith('op-1');
    expect(message.success).toHaveBeenCalled();
    expect(result.current.detailOpen).toBe(false);
    await waitFor(() => expect(result.current.pending).toEqual([]));
  });

  it('reports a cancel failure and keeps the drawer open', async () => {
    cancel.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useOperationProposals(true));
    await waitFor(() => expect(result.current.pendingLoading).toBe(false));

    await act(async () => {
      void result.current.openDetail(pendingProposal);
    });
    await act(async () => {
      await result.current.handleCancel();
    });
    expect(message.error).toHaveBeenCalled();
    expect(result.current.detailOpen).toBe(true);
  });
});

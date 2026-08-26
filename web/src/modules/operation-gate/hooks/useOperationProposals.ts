import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { operationProposalApi } from '../api/operationProposal.api';
import type { OperationProposal } from '../model/operationProposal';

import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export type OperationProposalTab = 'pending' | 'history';

const isPending = (p: OperationProposal): boolean =>
  p.status === 'proposed' || p.status === 'reviewing';

// 权限审批面板共享状态机：待审批/历史子 tab（与工具审批同构）。admin 待审批 =
// listPending（全租户 pending）；member（readonly）待审批 = listMine 客户端过滤
// proposed/reviewing。历史两角色都走 listHistory（后端按角色过滤，分页）。待审批
// 提案可取消：member 自撤、admin 代撤，均落 cancelled 终态。
export const useOperationProposals = (readonly: boolean) => {
  const [activeTab, setActiveTab] = useState<OperationProposalTab>('pending');

  const [pending, setPending] = useState<OperationProposal[]>([]);
  const [pendingLoading, setPendingLoading] = useState(true);

  const [history, setHistory] = useState<OperationProposal[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const [detail, setDetail] = useState<OperationProposal | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [note, setNote] = useState('');
  const [reviewing, setReviewing] = useState(false);
  const [approving, setApproving] = useState(false);
  const [rejecting, setRejecting] = useState(false);
  const [cancelling, setCancelling] = useState(false);

  // 每个数据源独立 seq：切 tab 时在途请求按自己的 seq 丢弃，各自 finally 只清自己的
  // loading，避免 pending 卡死在 spinner。
  const pendingSeqRef = useRef(0);
  const historySeqRef = useRef(0);

  const loadPending = useCallback(async () => {
    const seq = ++pendingSeqRef.current;
    setPendingLoading(true);
    try {
      const rows = readonly
        ? (await operationProposalApi.listMine()).filter(isPending)
        : await operationProposalApi.listPending();
      if (seq !== pendingSeqRef.current) return;
      setPending(rows);
    } catch (err) {
      if (seq !== pendingSeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载待审批列表失败', duration: 3 });
    } finally {
      if (seq === pendingSeqRef.current) setPendingLoading(false);
    }
  }, [readonly]);

  const loadHistory = useCallback(async (nextPage: number, nextPageSize: number) => {
    const seq = ++historySeqRef.current;
    setHistoryLoading(true);
    try {
      const data = await operationProposalApi.listHistory(nextPage, nextPageSize);
      if (seq !== historySeqRef.current) return;
      setHistory(data.proposals);
      setTotal(data.total);
    } catch (err) {
      if (seq !== historySeqRef.current) return;
      message.error({ content: (err as RequestError).response?.data?.error || '加载审批历史失败', duration: 3 });
    } finally {
      if (seq === historySeqRef.current) setHistoryLoading(false);
    }
  }, [setTotal]);

  useEffect(() => {
    void loadPending();
  }, [loadPending]);

  const switchTab = useCallback((next: OperationProposalTab) => {
    setActiveTab(next);
    if (next === 'history') {
      // 每次进入历史 tab 都从第 1 页开始，并同步分页 state，避免显示第 1 页数据
      // 却高亮上一次的页码。
      onChange(1, pageSize);
      void loadHistory(1, pageSize);
    }
  }, [onChange, pageSize, loadHistory]);

  const handleHistoryPageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void loadHistory(nextPage, nextPageSize);
  }, [onChange, loadHistory]);

  const openDetail = useCallback(async (proposal: OperationProposal) => {
    if (readonly) {
      // 行内数据已含展示所需字段（proposerId/reviewedBy/reviewNote/payloadSummary）。
      setDetail(proposal);
      setNote('');
      setDetailOpen(true);
      return;
    }
    try {
      const full = await operationProposalApi.get(proposal.id);
      setDetail(full);
      setNote('');
      setDetailOpen(true);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载操作详情失败', duration: 3 });
    }
  }, [readonly]);

  const closeDetail = useCallback(() => {
    setDetailOpen(false);
    setDetail(null);
  }, []);

  const handleReview = useCallback(async () => {
    if (!detail) return;
    setReviewing(true);
    try {
      await operationProposalApi.startReview(detail.id);
      message.success({ content: '已开始审批', duration: 2 });
      await loadPending();
      await openDetail(detail);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '开始审批失败', duration: 3 });
    } finally {
      setReviewing(false);
    }
  }, [detail, loadPending, openDetail]);

  const handleApprove = useCallback(async () => {
    if (!detail) return;
    setApproving(true);
    try {
      await operationProposalApi.approve(detail.id);
      message.success({ content: '已批准', duration: 2 });
      setDetailOpen(false);
      await loadPending();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '批准失败', duration: 3 });
    } finally {
      setApproving(false);
    }
  }, [detail, loadPending]);

  const handleReject = useCallback(async () => {
    if (!detail) return;
    setRejecting(true);
    try {
      await operationProposalApi.reject(detail.id, note.trim());
      message.success({ content: '已拒绝', duration: 2 });
      setDetailOpen(false);
      await loadPending();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '拒绝失败', duration: 3 });
    } finally {
      setRejecting(false);
    }
  }, [detail, note, loadPending]);

  const handleCancel = useCallback(async () => {
    if (!detail) return;
    setCancelling(true);
    try {
      await operationProposalApi.cancel(detail.id);
      message.success({ content: '已取消申请', duration: 2 });
      setDetailOpen(false);
      await loadPending();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '取消失败', duration: 3 });
    } finally {
      setCancelling(false);
    }
  }, [detail, loadPending]);

  return {
    activeTab,
    pending,
    pendingLoading,
    history,
    historyLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    detail,
    detailOpen,
    note,
    reviewing,
    approving,
    rejecting,
    cancelling,
    setNote,
    switchTab,
    handleHistoryPageChange,
    openDetail,
    closeDetail,
    handleReview,
    handleApprove,
    handleReject,
    handleCancel,
  };
};

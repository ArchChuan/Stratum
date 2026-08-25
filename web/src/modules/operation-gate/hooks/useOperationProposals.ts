import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { operationProposalApi } from '../api/operationProposal.api';
import type { OperationProposal } from '../model/operationProposal';

interface RequestError { response?: { data?: { error?: string } } }

// 权限审批面板共享状态机。admin 模式 listPending + 详情 get（含审批动作）；
// member（readonly）模式 listMine + 详情直接渲染行内数据——后端 get 是 reviewer
// 门控（admin/owner 才可），member 调 get 会被拒，故不走新路由。
// grant_editor 提案在 proposed 态直接可批准/拒绝：批准即授予白名单并落 executed
// 终态（无提案人 replay），无需 startReview。
export const useOperationProposals = (readonly: boolean) => {
  const [proposals, setProposals] = useState<OperationProposal[]>([]);
  const [loading, setLoading] = useState(true);
  const [detail, setDetail] = useState<OperationProposal | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [note, setNote] = useState('');
  const [reviewing, setReviewing] = useState(false);
  const [approving, setApproving] = useState(false);
  const [rejecting, setRejecting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setProposals(readonly
        ? await operationProposalApi.listMine()
        : await operationProposalApi.listPending());
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载操作提案失败', duration: 3 });
    } finally {
      setLoading(false);
    }
  }, [readonly]);

  useEffect(() => {
    void load();
  }, [load]);

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
      await load();
      await openDetail(detail);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '开始审批失败', duration: 3 });
    } finally {
      setReviewing(false);
    }
  }, [detail, load, openDetail]);

  const handleApprove = useCallback(async () => {
    if (!detail) return;
    setApproving(true);
    try {
      await operationProposalApi.approve(detail.id);
      message.success({ content: '已批准', duration: 2 });
      setDetailOpen(false);
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '批准失败', duration: 3 });
    } finally {
      setApproving(false);
    }
  }, [detail, load]);

  const handleReject = useCallback(async () => {
    if (!detail) return;
    setRejecting(true);
    try {
      await operationProposalApi.reject(detail.id, note.trim());
      message.success({ content: '已拒绝', duration: 2 });
      setDetailOpen(false);
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '拒绝失败', duration: 3 });
    } finally {
      setRejecting(false);
    }
  }, [detail, note, load]);

  return {
    proposals, loading, detail, detailOpen, note,
    reviewing, approving, rejecting,
    setNote, openDetail, closeDetail, handleReview, handleApprove, handleReject,
  };
};

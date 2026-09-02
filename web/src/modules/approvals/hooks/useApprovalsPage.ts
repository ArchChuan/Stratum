import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { approvalApi } from '../api';
import type { ApprovalDetail, ApprovalRow, ApprovalDecision } from '../api';

import { APPROVAL_POLL_MS } from '@/constants';
import { tenantApi, type Member } from '@/modules/iam';
import { usePagination } from '@/shared/hooks';
import { extractErrorMessage } from '@/shared/lib';

export type ApprovalsTab = 'pending' | 'history';

// 可指派审批人的角色白名单：后端 GET /tenant/members?role=admin,owner 按此过滤
// 且 SetAssignee 会再次校验（ErrApprovalAssigneeInvalid 兜底），前端不自行过滤。
const ASSIGNABLE_ROLES = 'admin,owner';

export const useApprovalsPage = () => {
  const [activeTab, setActiveTab] = useState<ApprovalsTab>('pending');

  const [pendingRows, setPendingRows] = useState<ApprovalRow[]>([]);
  const [pendingLoading, setPendingLoading] = useState(true);

  const [historyRows, setHistoryRows] = useState<ApprovalRow[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const { current: page, pageSize, total, setTotal, onChange, pageSizeOptions } = usePagination();

  const [detail, setDetail] = useState<ApprovalDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const [approvers, setApprovers] = useState<Member[]>([]);
  const [approversLoading, setApproversLoading] = useState(false);

  // 操作中的审批：key = `${operation}:${id}`，防止连点与跨行竞态。
  const [actionKey, setActionKey] = useState<string | null>(null);
  // 每个数据源独立 seq：切 tab 时在途请求按自己的 seq 丢弃，绝不跨列表互相失效，
  // 且各自 finally 永远清自己的 loading，避免 pending 卡死在 spinner。
  const pendingSeqRef = useRef(0);
  const historySeqRef = useRef(0);
  const detailSeqRef = useRef(0);
  // 轮询独立 seq：与手动 loadPending 的 pendingSeqRef 隔离，轮询不碰 loading
  // （避免表格每轮闪 spinner），且慢轮询响应不会覆盖新轮询数据。
  const pollSeqRef = useRef(0);

  const loadPending = useCallback(async () => {
    const seq = ++pendingSeqRef.current;
    setPendingLoading(true);
    try {
      const rows = await approvalApi.listPending();
      if (seq !== pendingSeqRef.current) return;
      setPendingRows(rows);
    } catch (err) {
      if (seq !== pendingSeqRef.current) return;
      message.error({ content: extractErrorMessage(err, '加载待审批列表失败'), duration: 3 });
    } finally {
      if (seq === pendingSeqRef.current) setPendingLoading(false);
    }
  }, []);

  // 轮询静默刷新（F1）：复用 listPending（后端该端点 pending-only，与铃铛同源），
  // 窗口聚焦/定时自动同步审批结果。失败静默忽略（顶栏铃铛同款语义），不设 loading，
  // 独立 pollSeq 防轮询竞态。
  const pollPending = useCallback(async () => {
    const seq = ++pollSeqRef.current;
    try {
      const rows = await approvalApi.listPending();
      if (seq === pollSeqRef.current) setPendingRows(rows);
    } catch {
      // 静默：下次轮询/聚焦自动重试。
    }
  }, []);

  const loadHistory = useCallback(async (nextPage: number, nextPageSize: number) => {
    const seq = ++historySeqRef.current;
    setHistoryLoading(true);
    try {
      const data = await approvalApi.listHistory(nextPage, nextPageSize);
      if (seq !== historySeqRef.current) return;
      setHistoryRows(data.approvals);
      setTotal(data.total);
    } catch (err) {
      if (seq !== historySeqRef.current) return;
      message.error({ content: extractErrorMessage(err, '加载审批历史失败'), duration: 3 });
    } finally {
      if (seq === historySeqRef.current) setHistoryLoading(false);
    }
  }, [setTotal]);

  // 候选审批人 = 租户 admin/owner。后端按 role 过滤返回完整集合（不分页），
  // 避免前端仅拉前 100 成员导致候选缺失；SetAssignee 仍独立校验兜底。
  const loadApprovers = useCallback(async () => {
    setApproversLoading(true);
    try {
      const pageData = await tenantApi.members(1, 100, ASSIGNABLE_ROLES);
      setApprovers(pageData.members);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载可指派成员失败'), duration: 3 });
    } finally {
      setApproversLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadPending();
    // 审批结果自动同步（F1）：approval approved → 后端续跑消费 → 列表自动刷新，
    // 免手动刷新。轮询静默失败不打扰；窗口聚焦时立即刷新避免切回后状态陈旧。
    const timer = window.setInterval(() => void pollPending(), APPROVAL_POLL_MS);
    const onFocus = () => void pollPending();
    window.addEventListener('focus', onFocus);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', onFocus);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅首挂载；loadPending/pollPending 引用稳定
  }, []);

  const switchTab = useCallback((next: ApprovalsTab) => {
    setActiveTab(next);
    if (next === 'history') {
      // 每次进入历史 tab 都从第 1 页开始，并同步分页 state，避免显示第 1 页数据
      // 却高亮上一次的页码。
      onChange(1, pageSize);
      void loadHistory(1, pageSize);
    }
  }, [loadHistory, pageSize, onChange]);

  const handleHistoryPageChange = useCallback((nextPage: number, nextPageSize: number) => {
    onChange(nextPage, nextPageSize);
    void loadHistory(nextPage, nextPageSize);
  }, [onChange, loadHistory]);

  const openDetail = useCallback(async (id: string) => {
    const seq = ++detailSeqRef.current;
    setDetail(null);
    setDetailLoading(true);
    try {
      const data = await approvalApi.getDetail(id);
      if (seq !== detailSeqRef.current) return;
      setDetail(data);
    } catch (err) {
      if (seq !== detailSeqRef.current) return;
      message.error({ content: extractErrorMessage(err, '加载审批详情失败'), duration: 3 });
    } finally {
      if (seq === detailSeqRef.current) setDetailLoading(false);
    }
  }, []);

  const closeDetail = useCallback(() => {
    // 递增 seq 丢弃在途详情响应，避免关闭后旧响应又把 Drawer 打开。
    ++detailSeqRef.current;
    setDetail(null);
  }, []);

  const refresh = useCallback(async (tab: ApprovalsTab) => {
    await loadPending();
    if (tab === 'history') await loadHistory(page, pageSize);
  }, [loadPending, loadHistory, page, pageSize]);

  const runAction = useCallback(async (
    operation: 'approve' | 'reject' | 'execute',
    id: string,
    run: () => Promise<void>,
  ): Promise<boolean> => {
    const key = `${operation}:${id}`;
    setActionKey(key);
    try {
      await run();
      message.success({ content: '操作成功', duration: 2 });
      void refresh(activeTab);
      return true;
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '操作失败'), duration: 3 });
      return false;
    } finally {
      setActionKey(null);
    }
  }, [activeTab, refresh]);

  const decide = useCallback(async (id: string, decision: ApprovalDecision, reason?: string) => {
    return runAction(decision === 'approved' ? 'approve' : 'reject', id, () =>
      approvalApi.decide(id, decision, reason));
  }, [runAction]);

  const execute = useCallback(async (id: string): Promise<boolean> => {
    return runAction('execute', id, async () => {
      await approvalApi.execute(id);
    });
  }, [runAction]);

  const assign = useCallback(async (id: string, assignedApprover: string): Promise<boolean> => {
    const key = `assign:${id}`;
    setActionKey(key);
    try {
      await approvalApi.setAssignee(id, assignedApprover);
      message.success({ content: '指派成功', duration: 2 });
      void refresh(activeTab);
      return true;
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '指派失败'), duration: 3 });
      return false;
    } finally {
      setActionKey(null);
    }
  }, [activeTab, refresh]);

  const isActionLoading = useCallback((operation: string, id: string) =>
    actionKey === `${operation}:${id}`, [actionKey]);

  return {
    activeTab,
    pendingRows,
    pendingLoading,
    historyRows,
    historyLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    detail,
    detailLoading,
    approvers,
    approversLoading,
    switchTab,
    handleHistoryPageChange,
    openDetail,
    closeDetail,
    decide,
    execute,
    assign,
    loadApprovers,
    isActionLoading,
  };
};

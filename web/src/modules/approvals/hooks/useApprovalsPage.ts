import { message } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { approvalApi } from '../api';
import type { ApprovalDetail, ApprovalRow, ApprovalDecision } from '../api';

import { tenantApi, type Member } from '@/modules/iam';
import { usePagination } from '@/shared/hooks';

interface RequestError { response?: { data?: { error?: string } } }

export type ApprovalsTab = 'pending' | 'history';

const errorMessage = (err: unknown, fallback: string): string =>
  (err as RequestError).response?.data?.error || fallback;

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

  const loadPending = useCallback(async () => {
    const seq = ++pendingSeqRef.current;
    setPendingLoading(true);
    try {
      const rows = await approvalApi.listPending();
      if (seq !== pendingSeqRef.current) return;
      setPendingRows(rows);
    } catch (err) {
      if (seq !== pendingSeqRef.current) return;
      message.error({ content: errorMessage(err, '加载待审批列表失败'), duration: 0 });
    } finally {
      if (seq === pendingSeqRef.current) setPendingLoading(false);
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
      message.error({ content: errorMessage(err, '加载审批历史失败'), duration: 0 });
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
      message.error({ content: errorMessage(err, '加载可指派成员失败'), duration: 0 });
    } finally {
      setApproversLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadPending();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅首次加载
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
      message.error({ content: errorMessage(err, '加载审批详情失败'), duration: 0 });
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
      message.error({ content: errorMessage(err, '操作失败'), duration: 0 });
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
      message.error({ content: errorMessage(err, '指派失败'), duration: 0 });
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

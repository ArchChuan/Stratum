import { useCallback, useEffect, useRef, useState } from 'react';

import { approvalApi } from '../api';
import type { ApprovalRow } from '../api';

import { APPROVAL_POLL_MS } from '@/constants';

// 顶栏铃铛数据源：复用 ListPending（后端已按身份过滤——member 仅本人发起的、admin 全量），
// 不新增端点。轮询频率由 APPROVAL_POLL_MS 控制；窗口聚焦时立即刷新，避免切回后角标陈旧。
export const useApprovalNotifications = () => {
  const [rows, setRows] = useState<ApprovalRow[]>([]);
  const [loading, setLoading] = useState(false);
  // 递增 seq 丢弃在途响应，避免轮询竞态用旧数据覆盖新数据。
  const seqRef = useRef(0);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      const data = await approvalApi.listPending();
      if (seq !== seqRef.current) return;
      // 显式过滤 pending-only：角标只计待审批，防未来端点改含 approved 待执行态时
      // 误把"已批准待执行"当待办数计入角标。
      setRows(data.filter((r) => r.status === 'pending'));
    } catch {
      // 铃铛轮询失败静默忽略，不弹错误打扰用户；下次轮询/聚焦时自动重试。
    } finally {
      if (seq === seqRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), APPROVAL_POLL_MS);
    const onFocus = () => void refresh();
    window.addEventListener('focus', onFocus);
    // 卸载后 in-flight setState 是 no-op（React 18 行为），无需额外作废；轮询竞态
    // 由 refresh 内自增 seq 丢弃过期响应兜底。
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', onFocus);
    };
  }, [refresh]);

  return { rows, loading, refresh };
};

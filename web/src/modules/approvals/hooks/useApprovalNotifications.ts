import { useCallback, useEffect, useRef, useState } from 'react';

import { approvalApi } from '../api';
import { riskLevelLabel, statusLabel as toolStatusLabel } from '../labels';

import { APPROVAL_POLL_MS } from '@/constants';
import { useTenantRole } from '@/modules/iam';
import { operationProposalApi } from '@/modules/operation-gate/api/operationProposal.api';
import {
  OP_TYPE_LABELS,
  STATUS_LABELS as PROPOSAL_STATUS_LABELS,
  proposalResourceLabel,
} from '@/modules/operation-gate/model/operationProposal';

export interface ApprovalBellItem {
  key: string;
  /** 条目来源：工具审批 vs 权限提案。 */
  kind: 'tool' | 'proposal';
  title: string;
  subtitle: string;
  /** 点击跳转目标：tools → /approvals；permission → /approvals?tab=permission。 */
  tab: 'tools' | 'permission';
}

const isPendingProposal = (p: { status: string }): boolean =>
  p.status === 'proposed' || p.status === 'reviewing';

// 顶栏铃铛数据源：工具审批复用 ListPending（后端已按身份过滤——member 仅本人、admin
// 全量）；权限提案 admin 拉全量待审批、member 拉我发起的并过滤 pending。合并成统一
// BellItem，角标 = 两者待审批数之和。轮询沿用 APPROVAL_POLL_MS + focus 刷新 +
// seq 防竞态；任一数据源失败静默忽略（下次轮询/聚焦自动重试）。
export const useApprovalNotifications = () => {
  const { isAdmin } = useTenantRole();
  const [items, setItems] = useState<ApprovalBellItem[]>([]);
  const [loading, setLoading] = useState(false);
  // 递增 seq 丢弃在途响应，避免轮询竞态用旧数据覆盖新数据。
  const seqRef = useRef(0);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      // 工具审批：显式过滤 pending-only，防未来端点改含 approved 待执行态时误计角标。
      const toolRows = (await approvalApi.listPending()).filter((r) => r.status === 'pending');
      const proposalRows = isAdmin
        ? await operationProposalApi.listPending()
        : (await operationProposalApi.listMine()).filter(isPendingProposal);
      if (seq !== seqRef.current) return;
      setItems([
        ...toolRows.map((r) => ({
          key: `tool:${r.id}`,
          kind: 'tool' as const,
          title: r.server_id ? `${r.tool_name} · ${r.server_id}` : r.tool_name,
          subtitle: `${riskLevelLabel(r.risk_level)} · ${toolStatusLabel(r.status)} · ${new Date(r.expires_at).toLocaleString()}`,
          tab: 'tools' as const,
        })),
        ...proposalRows.map((p) => ({
          key: `proposal:${p.id}`,
          kind: 'proposal' as const,
          title: `${OP_TYPE_LABELS[p.opType] ?? p.opType} · ${proposalResourceLabel(p)}`,
          subtitle: `${PROPOSAL_STATUS_LABELS[p.status] ?? p.status} · ${new Date(p.createdAt).toLocaleString()}`,
          tab: 'permission' as const,
        })),
      ]);
    } catch {
      // 铃铛轮询失败静默忽略，不弹错误打扰用户；下次轮询/聚焦时自动重试。
    } finally {
      if (seq === seqRef.current) setLoading(false);
    }
  }, [isAdmin]);

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

  return { items, loading, refresh };
};

import { Modal, Pagination, Table, Tabs, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';

import type { ApprovalDecision, ApprovalRow } from '../api';
import { ApprovalDetailDrawer } from '../components/ApprovalDetailDrawer';
import { DecideApprovalModal, type DecideTarget } from '../components/DecideApprovalModal';
import { buildHistoryColumns, buildPendingColumns } from '../components/approvalColumns';
import {
  useApprovalsPage, type ApprovalsTab,
} from '../hooks/useApprovalsPage';

import { useTenantRole } from '@/modules/iam';
import { OperationProposalsPanel } from '@/modules/operation-gate/components/OperationProposalsPanel';
import { EmptyHint } from '@/shared/ui';

// 审批中心顶层栏：工具审批（工具调用待办/历史）与权限审批（操作提案 + 白名单申请）。
type TopTab = 'tools' | 'permission';

export const ApprovalsPage = () => {
  const { isAdmin } = useTenantRole();
  const [searchParams, setSearchParams] = useSearchParams();
  const [topTab, setTopTab] = useState<TopTab>(
    searchParams.get('tab') === 'permission' ? 'permission' : 'tools',
  );

  // 铃铛点击权限提案会外部 navigate('/approvals?tab=permission')（不改本组件 state），
  // 必须监听 searchParams 同步顶层 tab，否则通知跳转后仍停留在「工具审批」，用户看不到
  // 待审批提案（bugfix：URL 驱动与点击驱动保持一致）。
  const urlTab = searchParams.get('tab');
  useEffect(() => {
    setTopTab(urlTab === 'permission' ? 'permission' : 'tools');
  }, [urlTab]);

  const {
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
  } = useApprovalsPage();

  const [decideTarget, setDecideTarget] = useState<DecideTarget | null>(null);
  const [decideReason, setDecideReason] = useState('');

  const openDecide = useCallback((id: string, decision: ApprovalDecision) => {
    setDecideReason('');
    setDecideTarget({ id, decision });
  }, []);

  const confirmDecide = useCallback(async () => {
    if (!decideTarget) return;
    const ok = await decide(decideTarget.id, decideTarget.decision, decideReason.trim() || undefined);
    if (ok) {
      setDecideTarget(null);
      setDecideReason('');
    }
  }, [decide, decideTarget, decideReason]);

  // 执行是不可逆的高危操作，先确认再执行；成功后关闭详情避免旧状态残留。
  const confirmExecute = useCallback((id: string) => {
    Modal.confirm({
      title: '确认执行',
      content: '将立即执行该工具调用，执行结果不可撤销。确认继续？',
      okText: '执行',
      okButtonProps: { danger: true },
      onOk: () => execute(id).then((ok) => {
        if (ok) closeDetail();
      }),
    });
  }, [execute, closeDetail]);

  // 顶层栏切换：同步 URL 使 ?tab=permission 可分享/刷新保持。
  const switchTopTab = useCallback((key: string) => {
    const next = key as TopTab;
    setTopTab(next);
    setSearchParams(next === 'permission' ? { tab: 'permission' } : {}, { replace: true });
  }, [setSearchParams]);

  const pendingColumns = buildPendingColumns({
    approvers,
    approversLoading,
    isActionLoading,
    onAssign: (id, approver) => void assign(id, approver),
    onLoadApprovers: () => void loadApprovers(),
    onOpenDetail: (id) => void openDetail(id),
    onOpenDecide: openDecide,
    // member 只读：不展示批准/拒绝/指派操作（M4 产品决策：待办收敛到铃铛+工作台，
    // 发起人仅查看自己发起的审批状态）。
    readonly: !isAdmin,
  });
  const historyColumns = buildHistoryColumns((id) => void openDetail(id));

  return (
    <div>
      <Tabs
        activeKey={topTab}
        onChange={switchTopTab}
        items={[
          { key: 'tools', label: '工具审批' },
          { key: 'permission', label: '权限审批' },
        ]}
      />

      {topTab === 'tools' ? (
        <>
          <div style={{ marginBottom: 16 }}>
            <Typography.Title level={4} style={{ margin: 0 }}>
              工具审批
            </Typography.Title>
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              {isAdmin
                ? '管理员审批 Agent 请求的高风险工具调用'
                : '查看我发起的审批请求与状态'}
            </Typography.Text>
          </div>

          <Tabs
            activeKey={activeTab}
            onChange={(key) => switchTab(key as ApprovalsTab)}
            items={[
              { key: 'pending', label: '待审批' },
              { key: 'history', label: '历史' },
            ]}
          />

          {activeTab === 'pending' ? (
            <Table<ApprovalRow>
              rowKey="id"
              dataSource={pendingRows}
              columns={pendingColumns}
              loading={pendingLoading}
              size="small"
              pagination={false}
              locale={{ emptyText: <EmptyHint title="暂无待审批" description="没有等待审批的工具调用" /> }}
            />
          ) : (
            <>
              <Table<ApprovalRow>
                rowKey="id"
                dataSource={historyRows}
                columns={historyColumns}
                loading={historyLoading}
                size="small"
                pagination={false}
                locale={{ emptyText: <EmptyHint title="没有审批历史" description="完成过审批后在此查看" /> }}
              />
              <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
                <Pagination
                  current={page}
                  pageSize={pageSize}
                  total={total}
                  pageSizeOptions={pageSizeOptions}
                  showSizeChanger
                  showTotal={(t) => `共 ${t} 条记录`}
                  onChange={handleHistoryPageChange}
                />
              </div>
            </>
          )}
        </>
      ) : (
        <OperationProposalsPanel readonly={!isAdmin} />
      )}

      <DecideApprovalModal
        target={decideTarget}
        reason={decideReason}
        confirmLoading={decideTarget ? isActionLoading(decideTarget.decision === 'approved' ? 'approve' : 'reject', decideTarget.id) : false}
        onReasonChange={setDecideReason}
        onConfirm={() => void confirmDecide()}
        onCancel={() => setDecideTarget(null)}
      />

      <ApprovalDetailDrawer
        detail={detail}
        loading={detailLoading}
        open={detail !== null}
        readonly={!isAdmin}
        executeLoading={detail ? isActionLoading('execute', detail.id) : false}
        onExecute={confirmExecute}
        onClose={closeDetail}
      />
    </div>
  );
};

export default ApprovalsPage;

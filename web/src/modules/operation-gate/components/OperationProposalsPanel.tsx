import { Button, Descriptions, Drawer, Input, Modal, Pagination, Space, Table, Tabs, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback } from 'react';

import { useOperationProposals, type OperationProposalTab } from '../hooks/useOperationProposals';
import {
  OP_TYPE_LABELS, STATUS_COLORS, STATUS_LABELS,
  proposalResourceLabel,
  type OperationProposal,
} from '../model/operationProposal';

interface OperationProposalsPanelProps {
  /** member 只读：仅看我发起的提案与状态；admin 审批全部提案。 */
  readonly?: boolean;
}

const typeLabel = (opType: string): string => OP_TYPE_LABELS[opType] || opType;
const statusLabel = (status: string): string => STATUS_LABELS[status] || status;
const statusColor = (status: string): string => STATUS_COLORS[status] || 'default';

const isGrantEditor = (p: OperationProposal | null): boolean => p?.opType === 'grant_editor';

// 可取消 = 仍在审批流中（proposed/reviewing）；终态（含 cancelled）不可再操作。
const isCancellable = (p: OperationProposal | null): boolean =>
  p?.status === 'proposed' || p?.status === 'reviewing';

export const OperationProposalsPanel = ({ readonly = false }: OperationProposalsPanelProps) => {
  const {
    activeTab, pending, pendingLoading, history, historyLoading,
    total, page, pageSize, pageSizeOptions,
    detail, detailOpen, note,
    reviewing, approving, rejecting, cancelling,
    setNote, switchTab, handleHistoryPageChange,
    openDetail, closeDetail, handleReview, handleApprove, handleReject, handleCancel,
  } = useOperationProposals(readonly);

  const confirmApprove = useCallback(() => {
    if (!detail) return;
    Modal.confirm({
      title: isGrantEditor(detail) ? '批准该权限申请？' : '批准该操作？',
      content: isGrantEditor(detail)
        ? '批准即授予申请人该资源的编辑/查看白名单（直接生效，无提案人 replay）。'
        : '批准后提案者可在 24 小时内执行一次该操作，审批本身不会直接执行。',
      okText: '批准',
      cancelText: '取消',
      onOk: () => void handleApprove(),
    });
  }, [detail, handleApprove]);

  const confirmReject = useCallback(() => {
    if (!detail) return;
    if (!note.trim()) {
      message.warning({ content: '拒绝必须填写原因', duration: 2 });
      return;
    }
    Modal.confirm({
      title: isGrantEditor(detail) ? '拒绝该权限申请？' : '拒绝该操作？',
      content: `拒绝原因：${note}`,
      okText: '拒绝',
      cancelText: '取消',
      onOk: () => void handleReject(),
    });
  }, [detail, note, handleReject]);

  // member 自撤 / admin 代撤都走同一终态 cancelled，语义略有差异，文案区分。
  const confirmCancel = useCallback(() => {
    if (!detail) return;
    Modal.confirm({
      title: readonly ? '取消该申请？' : '撤销该提案？',
      content: readonly
        ? '取消后该申请立即失效，可在历史中查看记录。'
        : '撤销后该提案立即进入终态，可在历史中查看记录。',
      okText: readonly ? '取消申请' : '撤销',
      cancelText: '返回',
      onOk: () => void handleCancel(),
    });
  }, [detail, readonly, handleCancel]);

  const columns: ColumnsType<OperationProposal> = [
    { title: '类型', dataIndex: 'opType', render: typeLabel },
    { title: '资源', key: 'resource', render: (_, p) => proposalResourceLabel(p) },
    { title: '状态', dataIndex: 'status', render: (s: string) => <Tag color={statusColor(s)}>{statusLabel(s)}</Tag> },
    { title: '提案人/申请人', dataIndex: 'proposerId' },
    { title: '创建时间', dataIndex: 'createdAt', render: (v: string) => new Date(v).toLocaleString() },
    {
      title: '操作',
      key: 'actions',
      render: (_, proposal) => <Button type="link" onClick={() => void openDetail(proposal)}>查看</Button>,
    },
  ];

  return (
    <section>
      <Typography.Title level={4} style={{ margin: 0 }}>
        {readonly ? '我的权限申请' : '权限审批'}
      </Typography.Title>
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>
        {readonly
          ? '查看我发起的编辑/查看权限申请与状态，可取消待审批的申请'
          : '审批成员申请的 Agent / Skill 编辑权限与文档查看权限（批准即授予白名单），以及工具操作提案'}
      </Typography.Text>

      <Tabs
        activeKey={activeTab}
        onChange={(key) => switchTab(key as OperationProposalTab)}
        items={[
          { key: 'pending', label: '待审批' },
          { key: 'history', label: '历史' },
        ]}
        style={{ marginBottom: 8 }}
      />

      {activeTab === 'pending' ? (
        <Table<OperationProposal>
          rowKey="id"
          columns={columns}
          dataSource={pending}
          loading={pendingLoading}
          pagination={false}
          locale={{ emptyText: readonly ? '暂无待审批的申请' : '暂无待审批的权限申请或操作提案' }}
        />
      ) : (
        <>
          <Table<OperationProposal>
            rowKey="id"
            columns={columns}
            dataSource={history}
            loading={historyLoading}
            pagination={false}
            locale={{ emptyText: '暂无审批历史' }}
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

      <Drawer
        title={detail ? `${typeLabel(detail.opType)} · ${proposalResourceLabel(detail)}` : '申请/操作详情'}
        open={detailOpen}
        width={560}
        onClose={closeDetail}
      >
        {detail && (
          <Space direction="vertical" style={{ width: '100%' }} size="large">
            <Descriptions column={1} size="small">
              <Descriptions.Item label="类型">{typeLabel(detail.opType)}</Descriptions.Item>
              <Descriptions.Item label="资源">{proposalResourceLabel(detail)}</Descriptions.Item>
              <Descriptions.Item label="提案人/申请人">{detail.proposerId}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={statusColor(detail.status)}>{statusLabel(detail.status)}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="审批人">{detail.reviewedBy || '-'}</Descriptions.Item>
              <Descriptions.Item label="审批备注">{detail.reviewNote || '-'}</Descriptions.Item>
              {detail.expiresAt && <Descriptions.Item label="批准有效期至">{new Date(detail.expiresAt).toLocaleString()}</Descriptions.Item>}
            </Descriptions>
            <div>
              <Typography.Text strong>申请/变更内容（已脱敏）</Typography.Text>
              <pre style={{ background: '#fafafa', padding: 12, borderRadius: 6, fontSize: 12, overflow: 'auto' }}>
                {JSON.stringify(detail.payloadSummary, null, 2)}
              </pre>
            </div>
            {!readonly && detail.status === 'proposed' && !isGrantEditor(detail) && (
              <Button block loading={reviewing} onClick={() => void handleReview()}>开始审批</Button>
            )}
            {!readonly && (detail.status === 'reviewing' || (detail.status === 'proposed' && isGrantEditor(detail))) && (
              <div>
                <Input.TextArea
                  rows={3}
                  maxLength={500}
                  placeholder="拒绝时必填原因（最多 500 字）"
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                />
                <Space style={{ marginTop: 12, width: '100%', justifyContent: 'flex-end' }}>
                  <Button loading={cancelling} onClick={() => void confirmCancel()}>撤销</Button>
                  <Button danger loading={rejecting} onClick={() => void confirmReject()}>拒绝</Button>
                  <Button type="primary" loading={approving} onClick={() => void confirmApprove()}>批准</Button>
                </Space>
              </div>
            )}
            {!readonly && !isCancellable(detail) && (
              <Typography.Text type="secondary">该提案已进入终态，不可再审批。</Typography.Text>
            )}
            {readonly && (
              <>
                {isCancellable(detail) && (
                  <Button block danger loading={cancelling} onClick={() => void confirmCancel()}>取消申请</Button>
                )}
                <Typography.Text type="secondary">
                  {isCancellable(detail)
                    ? '等待管理员审批，可在历史中查看审批结果。'
                    : '该提案已进入终态，可在历史中查看记录。'}
                </Typography.Text>
              </>
            )}
          </Space>
        )}
      </Drawer>
    </section>
  );
};

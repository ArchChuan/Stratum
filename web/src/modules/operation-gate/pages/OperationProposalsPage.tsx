import { Button, Descriptions, Drawer, Input, Modal, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';

import { operationProposalApi } from '../api/operationProposal.api';
import type { OperationProposal } from '../model/operationProposal';

interface RequestError { response?: { data?: { error?: string } } }

const OP_TYPE_LABELS: Record<string, string> = {
  revision_apply: '修订应用',
  cross_agent_delegate: '跨 Agent 委托',
  schedule_create: '定时任务创建',
  self_modify: '自修改',
};

const STATUS_LABELS: Record<string, string> = {
  proposed: '待审批',
  reviewing: '审批中',
  approved: '已批准',
  rejected: '已拒绝',
  executed: '已执行',
};

const STATUS_COLORS: Record<string, string> = {
  proposed: 'orange',
  reviewing: 'blue',
  approved: 'green',
  rejected: 'red',
  executed: 'default',
};

export const OperationProposalsPage = () => {
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
      setProposals(await operationProposalApi.listPending());
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载待审批操作失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const openDetail = useCallback(async (proposal: OperationProposal) => {
    try {
      const full = await operationProposalApi.get(proposal.id);
      setDetail(full);
      setNote('');
      setDetailOpen(true);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载操作详情失败', duration: 0 });
    }
  }, []);

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
      message.error({ content: (err as RequestError).response?.data?.error || '开始审批失败', duration: 0 });
    } finally {
      setReviewing(false);
    }
  }, [detail, load, openDetail]);

  const handleApprove = useCallback(() => {
    if (!detail) return;
    Modal.confirm({
      title: '批准该操作？',
      content: '批准后提案者可在 24 小时内执行一次该操作，审批本身不会直接执行。',
      okText: '批准',
      cancelText: '取消',
      onOk: async () => {
        setApproving(true);
        try {
          await operationProposalApi.approve(detail.id);
          message.success({ content: '操作已批准', duration: 2 });
          setDetailOpen(false);
          await load();
        } catch (err) {
          message.error({ content: (err as RequestError).response?.data?.error || '批准失败', duration: 0 });
        } finally {
          setApproving(false);
        }
      },
    });
  }, [detail, load]);

  const handleReject = useCallback(() => {
    if (!detail) return;
    if (!note.trim()) {
      message.warning({ content: '拒绝必须填写原因', duration: 2 });
      return;
    }
    Modal.confirm({
      title: '拒绝该操作？',
      content: `拒绝原因：${note}`,
      okText: '拒绝',
      cancelText: '取消',
      onOk: async () => {
        setRejecting(true);
        try {
          await operationProposalApi.reject(detail.id, note.trim());
          message.success({ content: '操作已拒绝', duration: 2 });
          setDetailOpen(false);
          await load();
        } catch (err) {
          message.error({ content: (err as RequestError).response?.data?.error || '拒绝失败', duration: 0 });
        } finally {
          setRejecting(false);
        }
      },
    });
  }, [detail, note, load]);

  const columns: ColumnsType<OperationProposal> = [
    { title: '类型', dataIndex: 'opType', render: (opType: string) => OP_TYPE_LABELS[opType] || opType },
    { title: 'Agent', dataIndex: 'agentId' },
    { title: '状态', dataIndex: 'status', render: (status: string) => <Tag color={STATUS_COLORS[status]}>{STATUS_LABELS[status] || status}</Tag> },
    { title: '提案人', dataIndex: 'proposerId' },
    { title: '创建时间', dataIndex: 'createdAt', render: (v: string) => new Date(v).toLocaleString() },
    {
      title: '操作',
      key: 'actions',
      render: (_, proposal) => <Button type="link" onClick={() => void openDetail(proposal)}>查看</Button>,
    },
  ];

  return <section>
    <Typography.Title level={4}>操作审批</Typography.Title>
    <Table<OperationProposal>
      rowKey="id"
      columns={columns}
      dataSource={proposals}
      loading={loading}
      pagination={false}
    />
    <Drawer
      title={detail ? `${OP_TYPE_LABELS[detail.opType] || detail.opType} · ${detail.id}` : '操作详情'}
      open={detailOpen}
      width={560}
      onClose={closeDetail}
    >
      {detail && <Space direction="vertical" style={{ width: '100%' }} size="large">
        <Descriptions column={1} size="small">
          <Descriptions.Item label="类型">{OP_TYPE_LABELS[detail.opType] || detail.opType}</Descriptions.Item>
          <Descriptions.Item label="Agent">{detail.agentId}</Descriptions.Item>
          <Descriptions.Item label="提案人">{detail.proposerId}</Descriptions.Item>
          <Descriptions.Item label="状态">
            <Tag color={STATUS_COLORS[detail.status]}>{STATUS_LABELS[detail.status] || detail.status}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="审批人">{detail.reviewedBy || '-'}</Descriptions.Item>
          <Descriptions.Item label="审批备注">{detail.reviewNote || '-'}</Descriptions.Item>
          {detail.expiresAt && <Descriptions.Item label="批准有效期至">{new Date(detail.expiresAt).toLocaleString()}</Descriptions.Item>}
        </Descriptions>
        <div>
          <Typography.Text strong>变更内容（已脱敏）</Typography.Text>
          <pre style={{ background: '#fafafa', padding: 12, borderRadius: 6, fontSize: 12, overflow: 'auto' }}>
            {JSON.stringify(detail.payloadSummary, null, 2)}
          </pre>
        </div>
        {detail.status === 'proposed' && (
          <Button block loading={reviewing} onClick={() => void handleReview()}>开始审批</Button>
        )}
        {detail.status === 'reviewing' && (
          <div>
            <Input.TextArea
              rows={3}
              maxLength={500}
              placeholder="拒绝时必填原因（最多 500 字）"
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
            <Space style={{ marginTop: 12, width: '100%', justifyContent: 'flex-end' }}>
              <Button danger loading={rejecting} onClick={() => void handleReject()}>拒绝</Button>
              <Button type="primary" loading={approving} onClick={() => void handleApprove()}>批准</Button>
            </Space>
          </div>
        )}
        {detail.status !== 'proposed' && detail.status !== 'reviewing' && (
          <Typography.Text type="secondary">该提案已进入终态，不可再审批。</Typography.Text>
        )}
      </Space>}
    </Drawer>
  </section>;
};

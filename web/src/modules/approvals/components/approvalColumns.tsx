import { Button, Select, Space, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import type { ApprovalDecision, ApprovalRow } from '../api';
import { APPROVAL_STATUS_COLORS, riskLevelLabel, statusLabel, subjectKindLabel } from '../labels';

export interface PendingColumnsContext {
  approvers: { user_id: string; github_login: string }[];
  approversLoading: boolean;
  isActionLoading: (operation: string, id: string) => boolean;
  onAssign: (id: string, approver: string) => void;
  onLoadApprovers: () => void;
  onOpenDetail: (id: string) => void;
  onOpenDecide: (id: string, decision: ApprovalDecision) => void;
  // member 只读视角（M4）：隐藏批准/拒绝/指派操作，仅保留详情。
  readonly?: boolean;
}

const assignableOptions = (approvers: PendingColumnsContext['approvers']) =>
  approvers.map((m) => ({ value: m.user_id, label: m.github_login || m.user_id }));

// 昵称回退链：后端解析的 display_name > 原始 user_id（M5）。
const displayName = (row: ApprovalRow): string => row.user_display_name || row.user_id;
const approverName = (row: ApprovalRow): string =>
  row.assigned_approver_name || row.assigned_approver || '未指派';

export const buildPendingColumns = (ctx: PendingColumnsContext): ColumnsType<ApprovalRow> => [
  {
    title: '类型',
    dataIndex: 'subject_kind',
    width: 110,
    render: (v: string) => subjectKindLabel(v),
  },
  {
    title: '工具/资源',
    dataIndex: 'tool_name',
    ellipsis: true,
    render: (v: string, record) => (
      <Space size={4}>
        <span>{v}</span>
        {record.server_id && (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {record.server_id}
          </Typography.Text>
        )}
      </Space>
    ),
  },
  {
    title: '风险',
    dataIndex: 'risk_level',
    width: 90,
    render: (v: string) => (
      <Tag color={v === 'destructive' ? 'red' : 'blue'}>{riskLevelLabel(v)}</Tag>
    ),
  },
  {
    title: '状态',
    dataIndex: 'status',
    width: 110,
    render: (v: string) => <Tag color={APPROVAL_STATUS_COLORS[v]}>{statusLabel(v)}</Tag>,
  },
  { title: '发起人', dataIndex: 'user_id', ellipsis: true, width: 140, render: (_, record) => displayName(record) },
  {
    title: '指派审批人',
    key: 'assigned_approver',
    width: 150,
    render: (_, record) =>
      ctx.readonly ? (
        <Typography.Text>{approverName(record)}</Typography.Text>
      ) : (
        <Select
          size="small"
          loading={ctx.approversLoading}
          value={record.assigned_approver}
          placeholder="未指派"
          style={{ width: 132 }}
          options={assignableOptions(ctx.approvers)}
          onChange={(v) => ctx.onAssign(record.id, v)}
          disabled={ctx.isActionLoading('assign', record.id)}
          onDropdownVisibleChange={(open) => {
            if (open && ctx.approvers.length === 0) ctx.onLoadApprovers();
          }}
        />
      ),
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => new Date(v).toLocaleString(),
  },
  {
    title: '操作',
    key: 'actions',
    width: ctx.readonly ? 80 : 200,
    render: (_, record) => (
      <Space size={0}>
        <Button type="link" size="small" onClick={() => ctx.onOpenDetail(record.id)}>
          详情
        </Button>
        {!ctx.readonly && record.status === 'pending' && (
          <>
            <Button
              type="link"
              size="small"
              disabled={ctx.isActionLoading('approve', record.id)}
              onClick={() => ctx.onOpenDecide(record.id, 'approved')}
            >
              批准
            </Button>
            <Button
              type="link"
              size="small"
              danger
              disabled={ctx.isActionLoading('reject', record.id)}
              onClick={() => ctx.onOpenDecide(record.id, 'rejected')}
            >
              拒绝
            </Button>
          </>
        )}
      </Space>
    ),
  },
];

export const buildHistoryColumns = (onOpenDetail: (id: string) => void): ColumnsType<ApprovalRow> => [
  {
    title: '类型',
    dataIndex: 'subject_kind',
    width: 110,
    render: (v: string) => subjectKindLabel(v),
  },
  {
    title: '工具/资源',
    dataIndex: 'tool_name',
    ellipsis: true,
    render: (v: string, record) => (
      <Space size={4}>
        <span>{v}</span>
        {record.server_id && (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {record.server_id}
          </Typography.Text>
        )}
      </Space>
    ),
  },
  {
    title: '风险',
    dataIndex: 'risk_level',
    width: 90,
    render: (v: string) => (
      <Tag color={v === 'destructive' ? 'red' : 'blue'}>{riskLevelLabel(v)}</Tag>
    ),
  },
  { title: '发起人', dataIndex: 'user_id', ellipsis: true, width: 140, render: (_, record) => displayName(record) },
  {
    title: '状态',
    dataIndex: 'status',
    width: 90,
    render: (v: string) => <Tag color={APPROVAL_STATUS_COLORS[v]}>{statusLabel(v)}</Tag>,
  },
  {
    title: '处理人',
    dataIndex: 'decided_by_name',
    width: 140,
    render: (_, record) => record.decided_by_name || record.decided_by || '-',
  },
  {
    title: '创建时间',
    dataIndex: 'created_at',
    width: 160,
    render: (v: string) => new Date(v).toLocaleString(),
  },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render: (_, record) => (
      <Button type="link" size="small" onClick={() => onOpenDetail(record.id)}>
        详情
      </Button>
    ),
  },
];

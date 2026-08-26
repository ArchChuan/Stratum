import { Alert, Button, Modal, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { extractErrorMessage } from '@/shared/lib';

const { Text, Paragraph } = Typography;

// 版本状态 → 标签展示。published 是当前生效版本，deprecated 是被新版本覆盖的
// 历史版本（可回滚）；未知状态回退原文展示，不阻塞未来新增状态。
const VERSION_STATUS_TAG: Record<string, { color: string; label: string }> = {
  published: { color: 'green', label: '已发布' },
  deprecated: { color: 'default', label: '历史' },
};

// VersionRow 是技能/agent 等资源版本历史的通用行：操作者列优先展示昵称
// createdByName，缺失回退原始 createdBy，再缺失显示占位符。
export interface VersionRow {
  id: string;
  versionNo?: number;
  status: string;
  isCurrent?: boolean;
  createdByName?: string;
  createdBy?: string;
  createdAt?: string;
  /** 是否展示回滚入口：由页面按「历史版本 + 当前用户可编辑」判定。 */
  canRollback?: boolean;
  summary?: Record<string, unknown>;
}

export interface VersionHistoryProps {
  rows: VersionRow[];
  loading?: boolean;
  /** 回滚动作由页面注入：调用对应 API 并刷新数据；未注入则不渲染回滚入口。 */
  rollback?: (row: VersionRow) => Promise<void>;
  /** 顶部说明文案；不传则不渲染 Alert。 */
  infoMessage?: string;
}

// VersionHistory 是技能/agent 共用的版本历史展示：当前生效标记、操作者昵称、
// 时间与回滚入口。回滚确认 Modal 内置于组件，成功后执行页面注入的 rollback
// （API 调用 + 数据刷新），失败错误提示在组件内兜底。
export const VersionHistory = ({ rows, loading = false, rollback, infoMessage }: VersionHistoryProps) => {
  const confirmRollback = (row: VersionRow) => {
    Modal.confirm({
      title: `回滚到版本 v${row.versionNo ?? '—'}？`,
      content: '回滚后该版本立即生效：当前版本标记为历史，不产生新版本，历史保留可再次回滚。',
      okText: '回滚', okButtonProps: { danger: true }, cancelText: '取消',
      onOk: async () => {
        if (!rollback) return;
        try {
          await rollback(row);
          message.success({ content: `已回滚到版本 v${row.versionNo ?? '—'}`, duration: 2 });
        } catch (err) {
          message.error({ content: extractErrorMessage(err, '回滚失败'), duration: 3 });
        }
      },
    });
  };

  const columns: ColumnsType<VersionRow> = [
    { title: '版本', dataIndex: 'versionNo', width: 80, render: (no: number) => `v${no ?? '—'}` },
    { title: '状态', dataIndex: 'status', width: 150, render: (_: unknown, r: VersionRow) => (
      <>
        {r.isCurrent && <Tag color="blue" style={{ marginInlineEnd: 4 }}>当前生效</Tag>}
        <Tag color={VERSION_STATUS_TAG[r.status]?.color}>{VERSION_STATUS_TAG[r.status]?.label ?? r.status}</Tag>
      </>
    ) },
    { title: '操作者', dataIndex: 'createdBy', width: 140, render: (_: unknown, r: VersionRow) => r.createdByName || r.createdBy || <Text type="secondary">—</Text> },
    { title: '时间', dataIndex: 'createdAt', width: 180, render: (t: string) => (t ? new Date(t).toLocaleString('zh-CN', { hour12: false }) : <Text type="secondary">—</Text>) },
    { title: '操作', key: 'actions', width: 100, render: (_: unknown, r: VersionRow) => (
      r.canRollback && rollback ? (
        <Button type="link" size="small" danger onClick={() => confirmRollback(r)}>回滚</Button>
      ) : null
    ) },
  ];

  return (
    <div style={{ maxWidth: 720 }}>
      {infoMessage && <Alert type="info" showIcon style={{ marginBottom: 16 }} message={infoMessage} />}
      <Table<VersionRow> rowKey="id" size="small" loading={loading} columns={columns} dataSource={rows}
        pagination={{ pageSize: 5, showSizeChanger: false }}
        locale={{ emptyText: <Paragraph type="secondary" style={{ padding: 16 }}>暂无版本记录</Paragraph> }} />
    </div>
  );
};

export default VersionHistory;

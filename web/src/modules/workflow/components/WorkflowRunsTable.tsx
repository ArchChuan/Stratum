import { EyeOutlined } from '@ant-design/icons';
import { Button, Card, Progress, Space, Tag, Typography } from 'antd';
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table';

import type { WorkflowRunSummary } from '../model/workflow';

import { ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;
const statusLabels: Record<string, string> = {
  queued: '排队中', running: '运行中', completed: '已完成', failed: '失败', paused: '已暂停',
  pause_requested: '暂停中', cancel_requested: '取消中', canceled: '已取消', manual_intervention: '等待人工处理',
};
const statusColors: Record<string, string> = { running: 'processing', completed: 'success', failed: 'error', paused: 'warning', manual_intervention: 'warning' };
const formatTime = (value?: string | null) => value ? new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value)) : '—';

export const WorkflowRunsTable = ({ runs, loading, pagination, emptyText, onOpen, onPageChange }: {
  runs: WorkflowRunSummary[];
  loading: boolean;
  pagination: TablePaginationConfig;
  emptyText: string;
  onOpen: (run: WorkflowRunSummary) => void;
  onPageChange: (page: number, pageSize: number) => void;
}) => {
  const columns: ColumnsType<WorkflowRunSummary> = [
    { title: '工作流', dataIndex: 'definition_id', key: 'definition_id', ellipsis: true, render: (value, run) => <div><Text strong>{value}</Text><div><Text type="secondary">版本 {run.version}</Text></div></div> },
    { title: '状态', dataIndex: 'status', key: 'status', width: 150, render: (status) => <Space direction="vertical" size={2}><Tag color={statusColors[status]}>{statusLabels[status] || status}</Tag><Progress percent={status === 'completed' ? 100 : status === 'running' ? 50 : 0} showInfo={false} size="small" /></Space> },
    { title: '开始时间', dataIndex: 'started_at', key: 'started_at', width: 150, render: formatTime },
    { title: '完成时间', dataIndex: 'finished_at', key: 'finished_at', width: 150, render: formatTime },
    { title: '操作', key: 'actions', width: 90, render: (_, run) => <Button aria-label="查看运行" type="link" icon={<EyeOutlined />} onClick={() => onOpen(run)}>查看</Button> },
  ];
  return <ResponsiveDataView
    rows={runs} columns={columns} rowKey="id" loading={loading} pagination={pagination} emptyText={emptyText}
    mobilePaginationMode="server" onChange={(next) => onPageChange(next.current || 1, next.pageSize || 20)}
    renderMobileItem={(run) => <Card><Space direction="vertical"><Text strong>{run.definition_id}</Text><Tag color={statusColors[run.status]}>{statusLabels[run.status]}</Tag><Text type="secondary">{formatTime(run.started_at)}</Text><Button aria-label="查看运行" onClick={() => onOpen(run)}>查看详情</Button></Space></Card>}
  />;
};

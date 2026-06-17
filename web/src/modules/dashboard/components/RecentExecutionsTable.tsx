import { Card, Table, Tag, Typography } from 'antd';

import type { DashboardExecution } from '../model/dashboard';

const { Text } = Typography;

const statusColors: Record<string, string> = { success: 'success', error: 'error' };
const statusLabels: Record<string, string> = { success: '成功', error: '失败' };

const formatDuration = (ms?: number) => {
  if (!ms) return '-';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
};

const columns = [
  {
    title: 'Agent',
    dataIndex: 'agent_name',
    key: 'agent_name',
    width: 140,
    ellipsis: true,
    render: (v: string) => <Text strong>{v}</Text>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    key: 'status',
    width: 70,
    render: (s: string) => <Tag color={statusColors[s] || 'default'}>{statusLabels[s] || s}</Tag>,
  },
  {
    title: '输入',
    dataIndex: 'input_preview',
    key: 'input_preview',
    ellipsis: true,
    render: (v?: string) => (
      <Text type="secondary" ellipsis>
        {v || '-'}
      </Text>
    ),
  },
  {
    title: '输出',
    dataIndex: 'output_preview',
    key: 'output_preview',
    ellipsis: true,
    render: (text: string | undefined, record: DashboardExecution) =>
      record.status === 'error' ? (
        <Text type="danger" ellipsis>
          {record.error_message || '执行失败'}
        </Text>
      ) : (
        <Text ellipsis>{text || '-'}</Text>
      ),
  },
  {
    title: 'Token',
    dataIndex: 'total_tokens',
    key: 'total_tokens',
    width: 80,
    align: 'right' as const,
    render: (v?: number) => (v ? <Text>{v.toLocaleString()}</Text> : '-'),
  },
  {
    title: '耗时',
    dataIndex: 'duration_ms',
    key: 'duration_ms',
    width: 80,
    align: 'right' as const,
    render: (v?: number) => <Text>{formatDuration(v)}</Text>,
  },
  {
    title: '时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 150,
    render: (d: string) => <Text type="secondary">{new Date(d).toLocaleString('zh-CN')}</Text>,
  },
];

interface RecentExecutionsTableProps {
  data: DashboardExecution[];
  loading: boolean;
}

export const RecentExecutionsTable = ({ data, loading }: RecentExecutionsTableProps) => (
  <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
    <Table
      dataSource={data}
      columns={columns}
      rowKey="id"
      loading={loading}
      pagination={false}
      locale={{ emptyText: '暂无执行记录' }}
      size="small"
      style={{ borderRadius: 12, overflow: 'hidden' }}
    />
  </Card>
);

export default RecentExecutionsTable;

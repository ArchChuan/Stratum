import { CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { Card, Flex, Space, Tag, Typography } from 'antd';
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table';

import type { ExecutionRow } from '../model/execution';

import { PAGE_SIZE_OPTIONS } from '@/constants';
import { ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;

const formatDuration = (ms?: number) => {
  if (!ms) return '-';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
};

const columns: ColumnsType<ExecutionRow> = [
  {
    title: 'Agent',
    dataIndex: 'agent_name',
    key: 'agent_name',
    width: 140,
    ellipsis: true,
    render: (v: string) => <Text strong>{v || '-'}</Text>,
  },
  {
    title: '状态',
    dataIndex: 'status',
    key: 'status',
    width: 88,
    filters: [
      { text: '成功', value: 'success' },
      { text: '失败', value: 'error' },
    ],
    onFilter: (value, record) => record.status === value,
    render: (s: string) =>
      s === 'success' ? (
        <Tag icon={<CheckCircleOutlined />} color="success">
          成功
        </Tag>
      ) : (
        <Tag icon={<CloseCircleOutlined />} color="error">
          失败
        </Tag>
      ),
  },
  {
    title: '输入预览',
    dataIndex: 'input_preview',
    key: 'input_preview',
    ellipsis: true,
    render: (v: string) => <Text ellipsis>{v || '-'}</Text>,
  },
  {
    title: '输出预览',
    dataIndex: 'output_preview',
    key: 'output_preview',
    ellipsis: true,
    render: (text: string, record) =>
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
    width: 90,
    align: 'right',
    sorter: (a, b) => (a.total_tokens || 0) - (b.total_tokens || 0),
    render: (v: number) => (v ? v.toLocaleString() : '-'),
  },
  {
    title: '耗时',
    dataIndex: 'duration_ms',
    key: 'duration_ms',
    width: 90,
    align: 'right',
    sorter: (a, b) => (a.duration_ms || 0) - (b.duration_ms || 0),
    render: (v: number) => formatDuration(v),
  },
  {
    title: '时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 160,
    sorter: (a, b) =>
      new Date(a.created_at || 0).getTime() - new Date(b.created_at || 0).getTime(),
    defaultSortOrder: 'descend',
    render: (d: string) => (
      <Text type="secondary">{d ? new Date(d).toLocaleString('zh-CN') : '-'}</Text>
    ),
  },
];

interface ExecutionHistoryTableProps {
  executions: ExecutionRow[];
  loading: boolean;
  pagination: { current: number; pageSize: number; total: number };
  onChange: (pag: TablePaginationConfig) => void;
}

export const ExecutionHistoryTable = ({
  executions,
  loading,
  pagination,
  onChange,
}: ExecutionHistoryTableProps) => (
  <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
    <ResponsiveDataView<ExecutionRow>
      rows={executions}
      columns={columns}
      rowKey="id"
      loading={loading}
      emptyText="暂无执行记录"
      mobilePaginationMode="server"
      pagination={{
        current: pagination.current,
        pageSize: pagination.pageSize,
        total: pagination.total,
        showSizeChanger: true,
        pageSizeOptions: PAGE_SIZE_OPTIONS,
        showQuickJumper: true,
        showTotal: (total) => `共 ${total} 条`,
        style: { padding: '12px 16px' },
      }}
      onChange={onChange}
      renderMobileItem={(execution) => (
        <div style={{ padding: 12, borderBottom: '1px solid #f0f0f0' }}>
          <Flex justify="space-between" align="center" gap={8}>
            <Text strong ellipsis>{execution.agent_name || '-'}</Text>
            {execution.status === 'success' ? (
              <Tag icon={<CheckCircleOutlined />} color="success">成功</Tag>
            ) : (
              <Tag icon={<CloseCircleOutlined />} color="error">失败</Tag>
            )}
          </Flex>
          <Text ellipsis style={{ display: 'block', marginTop: 8 }}>
            {execution.input_preview || '-'}
          </Text>
          {execution.status === 'error' && (
            <Text type="danger" ellipsis style={{ display: 'block', marginTop: 4 }}>
              {execution.error_message || '执行失败'}
            </Text>
          )}
          <Flex justify="space-between" align="center" gap={8} style={{ marginTop: 10 }}>
            <Space size={12}>
              <Text>{execution.total_tokens ? `${execution.total_tokens.toLocaleString()} Token` : '-'}</Text>
              <Text>{formatDuration(execution.duration_ms)}</Text>
            </Space>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {execution.created_at ? new Date(execution.created_at).toLocaleString('zh-CN') : '-'}
            </Text>
          </Flex>
        </div>
      )}
    />
  </Card>
);

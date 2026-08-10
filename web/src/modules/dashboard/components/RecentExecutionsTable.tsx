import { Card, Flex, Space, Tag, Typography } from 'antd';
import { useState } from 'react';

import type { DashboardExecution } from '../model/dashboard';

import { ExecutionDetailDrawer } from './ExecutionDetailDrawer';
import { formatDuration, statusColors, statusLabels } from './executionFormat';

import { PAGE_SIZE_OPTIONS } from '@/constants';
import { ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;

const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN');

// 列表页 ≤5 列：Agent / 状态 / 摘要(输入输出合并截断) / Token / 时间，
// 完整信息（含 trace_id、耗时）下放 ExecutionDetailDrawer。
const columns = [
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
    width: 70,
    render: (s: string) => <Tag color={statusColors[s] || 'default'}>{statusLabels[s] || s}</Tag>,
  },
  {
    title: '摘要',
    dataIndex: 'input_preview',
    key: 'summary',
    ellipsis: true,
    render: (_: string | undefined, record: DashboardExecution) => (
      <Flex vertical gap={2}>
        <Text type="secondary" ellipsis>
          {record.input_preview || '-'}
        </Text>
        {record.status === 'error' ? (
          <Text type="danger" ellipsis>
            {record.error_message || '执行失败'}
          </Text>
        ) : (
          <Text ellipsis>{record.output_preview || '-'}</Text>
        )}
      </Flex>
    ),
  },
  {
    title: 'Token',
    dataIndex: 'total_tokens',
    key: 'total_tokens',
    width: 90,
    align: 'right' as const,
    render: (v?: number) => (v ? <Text>{v.toLocaleString()}</Text> : '-'),
  },
  {
    title: '时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 150,
    render: (d: string) => <Text type="secondary">{formatTime(d)}</Text>,
  },
];

interface RecentExecutionsTableProps {
  data: DashboardExecution[];
  loading: boolean;
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (page: number, pageSize: number) => void;
}

export const RecentExecutionsTable = ({
  data,
  loading,
  total,
  page,
  pageSize,
  onPageChange,
}: RecentExecutionsTableProps) => {
  const [selected, setSelected] = useState<DashboardExecution | null>(null);

  return (
    <>
      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
        <ResponsiveDataView
          rows={data}
          columns={columns}
          rowKey="id"
          loading={loading}
          size="small"
          pagination={{
            current: page,
            pageSize,
            total,
            pageSizeOptions: PAGE_SIZE_OPTIONS,
            showSizeChanger: true,
            onChange: onPageChange,
          }}
          onRow={(execution) => ({ onClick: () => setSelected(execution) })}
          emptyText="暂无执行记录"
          renderMobileItem={(execution) => (
            <div
              style={{ padding: 12, borderBottom: '1px solid #f0f0f0', cursor: 'pointer' }}
              onClick={() => setSelected(execution)}
            >
              <Flex justify="space-between" align="center" gap={8}>
                <Text strong ellipsis>{execution.agent_name || '-'}</Text>
                <Tag color={statusColors[execution.status] || 'default'}>
                  {statusLabels[execution.status] || execution.status}
                </Tag>
              </Flex>
              <Text type="secondary" ellipsis style={{ display: 'block', marginTop: 8 }}>
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
                  {formatTime(execution.created_at)}
                </Text>
              </Flex>
            </div>
          )}
        />
      </Card>
      <ExecutionDetailDrawer execution={selected} onClose={() => setSelected(null)} />
    </>
  );
};

export default RecentExecutionsTable;

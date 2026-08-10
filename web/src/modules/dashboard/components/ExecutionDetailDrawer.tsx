import { Descriptions, Drawer, Tag, Typography } from 'antd';

import type { DashboardExecution } from '../model/dashboard';

import { formatDuration, statusColors, statusLabels } from './executionFormat';

const { Text } = Typography;

interface ExecutionDetailDrawerProps {
  execution: DashboardExecution | null;
  onClose: () => void;
}

const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN');

export const ExecutionDetailDrawer = ({ execution, onClose }: ExecutionDetailDrawerProps) => (
  <Drawer title="执行详情" width={480} open={execution !== null} onClose={onClose}>
    {execution && (
      <Descriptions column={1} size="small" items={[
        {
          key: 'agent',
          label: 'Agent',
          children: <Text strong>{execution.agent_name || '-'}</Text>,
        },
        {
          key: 'status',
          label: '状态',
          children: (
            <Tag color={statusColors[execution.status] || 'default'}>
              {statusLabels[execution.status] || execution.status}
            </Tag>
          ),
        },
        { key: 'input', label: '输入', children: execution.input_preview || '-' },
        {
          key: 'output',
          label: '输出',
          children: execution.status === 'error' ? (
            <Text type="danger">{execution.error_message || '执行失败'}</Text>
          ) : (
            execution.output_preview || '-'
          ),
        },
        {
          key: 'tokens',
          label: 'Token 数',
          children: execution.total_tokens ? execution.total_tokens.toLocaleString() : '-',
        },
        { key: 'duration', label: '耗时', children: formatDuration(execution.duration_ms) },
        { key: 'time', label: '执行时间', children: formatTime(execution.created_at) },
        { key: 'traceId', label: 'Trace ID', children: execution.trace_id || '-' },
        { key: 'agentId', label: 'Agent ID', children: execution.agent_id || '-' },
        { key: 'executionId', label: '执行 ID', children: execution.id },
      ]}
      />
    )}
  </Drawer>
);

export default ExecutionDetailDrawer;

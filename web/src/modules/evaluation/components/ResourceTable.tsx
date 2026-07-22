import { Button, Empty, Table, Tag, Typography } from 'antd';

import type { ResourceSummary } from '../model/evaluation';

import { displayLabel, StatusTag } from './evaluationView';

interface Props {
  resources: ResourceSummary[];
  loading: boolean;
  filtered: boolean;
  onOpen: (resource: ResourceSummary) => void;
}

export const ResourceTable = ({ resources, loading, filtered, onOpen }: Props) => (
  <Table<ResourceSummary>
    size="small" rowKey="id" dataSource={resources} loading={loading} pagination={false}
    locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE}
      description={filtered ? '没有找到符合条件的评测资源' : '评测资源还是空的'} /> }}
    columns={[
      { title: '资源', dataIndex: 'resource_id', render: (value: string, row) => <>
        <Typography.Text strong>{String(row.safe_summary.name || row.safe_summary.label || value)}</Typography.Text>
        <Typography.Text type="secondary" style={{ display: 'block' }}>{value}</Typography.Text>
      </> },
      { title: '类型', dataIndex: 'resource_kind', width: 100, render: (value: string) => <Tag>{displayLabel(value)}</Tag> },
      { title: '状态', dataIndex: 'status', width: 110, render: (value: string) => <StatusTag value={value} /> },
      { title: '最近运行', dataIndex: 'latest_run_status', width: 120, render: (value?: string) => <StatusTag value={value} /> },
      { title: '操作', width: 90, fixed: 'right', render: (_, row) => <Button type="link" size="small"
        aria-label={`查看 ${row.resource_id}`} onClick={() => onOpen(row)}>详情</Button> },
    ]}
    scroll={{ x: 680 }}
  />
);

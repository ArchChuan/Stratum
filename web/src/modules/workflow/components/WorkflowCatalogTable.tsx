import { DeleteOutlined, EditOutlined, EyeOutlined, PlayCircleOutlined } from '@ant-design/icons';
import { Button, Card, Space, Tag, Typography } from 'antd';
import type { ColumnsType, TablePaginationConfig } from 'antd/es/table';

import type { WorkflowSummary } from '../model/workflow';

import { DangerPopconfirm, ResponsiveDataView } from '@/shared/ui';

const { Paragraph, Text } = Typography;

const formatTime = (value: string) => new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
}).format(new Date(value));

export const WorkflowCatalogTable = ({
  workflows,
  loading,
  pagination,
  emptyText,
  canManage,
  onView,
  onRun,
  onEdit,
  onDelete,
  onPageChange,
}: {
  workflows: WorkflowSummary[];
  loading: boolean;
  pagination: TablePaginationConfig;
  emptyText: string;
  canManage: boolean;
  onView: (workflow: WorkflowSummary) => void;
  onRun: (workflow: WorkflowSummary) => void;
  onEdit: (workflow: WorkflowSummary) => void;
  onDelete: (workflow: WorkflowSummary) => void;
  onPageChange: (page: number, pageSize: number) => void;
}) => {
  const columns: ColumnsType<WorkflowSummary> = [
    {
      title: '工作流', dataIndex: 'name', key: 'name',
      render: (_, workflow) => <div><Text strong>{workflow.name}</Text><Paragraph ellipsis={{ rows: 1 }}>{workflow.description || '暂无说明'}</Paragraph></div>,
    },
    { title: '草稿', dataIndex: 'revision', key: 'revision', width: 110, render: (revision) => <Tag>修订 {revision}</Tag> },
    { title: '更新时间', dataIndex: 'updated_at', key: 'updated_at', width: 150, render: formatTime },
    {
      title: '操作', key: 'actions', width: canManage ? 420 : 200,
      render: (_, workflow) => <Space>
        <Button aria-label="查看详情" type="link" icon={<EyeOutlined />} onClick={() => onView(workflow)}>详情</Button>
        <Button aria-label="运行工作流" type="link" icon={<PlayCircleOutlined />} onClick={() => onRun(workflow)}>运行工作流</Button>
        {canManage && <Button aria-label="编辑草稿" type="link" icon={<EditOutlined />} onClick={() => onEdit(workflow)}>编辑草稿</Button>}
        {canManage && <DangerPopconfirm
          title="确认删除这个工作流草稿？"
          description="已发布或已有运行记录的工作流无法删除。"
          okText="确认删除"
          onConfirm={() => onDelete(workflow)}
        >
          <Button aria-label="删除草稿" type="link" danger icon={<DeleteOutlined />}>删除草稿</Button>
        </DangerPopconfirm>}
      </Space>,
    },
  ];

  return <ResponsiveDataView
    rows={workflows}
    columns={columns}
    rowKey="id"
    loading={loading}
    emptyText={emptyText}
    pagination={pagination}
    mobilePaginationMode="server"
    onChange={(next) => onPageChange(next.current || 1, next.pageSize || 20)}
    renderMobileItem={(workflow) => <Card className="workflow-catalog-card">
      <div className="workflow-card-spine" />
      <Text strong>{workflow.name}</Text>
      <Paragraph>{workflow.description || '暂无说明'}</Paragraph>
      <Space><Tag>修订 {workflow.revision}</Tag><Text type="secondary">{formatTime(workflow.updated_at)}</Text></Space>
      <div className="workflow-card-actions">
        <Button aria-label="查看详情" icon={<EyeOutlined />} onClick={() => onView(workflow)}>详情</Button>
        <Button aria-label="运行工作流" type="primary" ghost icon={<PlayCircleOutlined />} onClick={() => onRun(workflow)}>运行工作流</Button>
        {canManage && <Button aria-label="编辑草稿" icon={<EditOutlined />} onClick={() => onEdit(workflow)}>编辑草稿</Button>}
        {canManage && <DangerPopconfirm
          title="确认删除这个工作流草稿？"
          description="已发布或已有运行记录的工作流无法删除。"
          okText="确认删除"
          onConfirm={() => onDelete(workflow)}
        >
          <Button aria-label="删除草稿" danger icon={<DeleteOutlined />}>删除草稿</Button>
        </DangerPopconfirm>}
      </div>
    </Card>}
  />;
};

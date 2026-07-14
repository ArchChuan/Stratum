import { Badge, Card, Flex, Progress, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import type { KnowledgeDocument } from '../model/knowledge';

import { COMPACT_PAGE_SIZE } from '@/constants';
import { ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;

interface WorkspaceDocumentsTableProps {
  documents: KnowledgeDocument[];
  loading: boolean;
}

const STATUS_META: Record<string, { color: string; label: string }> = {
  processing: { color: 'processing', label: '处理中' },
  completed: { color: 'success', label: '已完成' },
  failed: { color: 'error', label: '失败' },
};

const renderStatus = (doc: KnowledgeDocument) => {
  const meta = STATUS_META[doc.ingest_status] ?? { color: 'default', label: doc.ingest_status };
  if (doc.ingest_status === 'failed' && doc.ingest_error) {
    return (
      <Tooltip title={doc.ingest_error}>
        <Tag color={meta.color}>{meta.label}</Tag>
      </Tooltip>
    );
  }
  return <Tag color={meta.color}>{meta.label}</Tag>;
};

const renderProgress = (doc: KnowledgeDocument) => {
  const total = doc.total_chunks || 0;
  const done = doc.processed_chunks || 0;
  if (total === 0) return <Text type="secondary">-</Text>;
  if (doc.ingest_status === 'completed') return <Text>{total}</Text>;
  if (doc.ingest_status === 'failed') {
    return (
      <Text type="secondary">
        {done} / {total}
      </Text>
    );
  }
  const percent = Math.min(100, Math.round((done / total) * 100));
  return (
    <Progress
      percent={percent}
      size="small"
      status="active"
      format={() => `${done}/${total}`}
    />
  );
};

const columns: ColumnsType<KnowledgeDocument> = [
  {
    title: '文件名',
    dataIndex: 'source',
    key: 'source',
    ellipsis: true,
    render: (source: string) => <Text>{source || '-'}</Text>,
  },
  {
    title: '状态',
    key: 'ingest_status',
    width: 120,
    render: (_, doc) => renderStatus(doc),
  },
  {
    title: '分块进度',
    key: 'progress',
    width: 220,
    render: (_, doc) => renderProgress(doc),
  },
  {
    title: '开始时间',
    dataIndex: 'ingest_started_at',
    key: 'ingest_started_at',
    width: 180,
    render: (t: string | null | undefined) =>
      t ? new Date(t).toLocaleString('zh-CN') : <Text type="secondary">-</Text>,
  },
];

export const WorkspaceDocumentsTable = ({ documents, loading }: WorkspaceDocumentsTableProps) => (
  <Card
    title="文档"
    extra={<Badge count={documents.length} style={{ backgroundColor: '#d9d9d9', color: '#595959' }} />}
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
  >
    <ResponsiveDataView<KnowledgeDocument>
      rowKey="id"
      loading={loading}
      size="small"
      rows={documents}
      columns={columns}
      pagination={{ pageSize: COMPACT_PAGE_SIZE, size: 'small' }}
      emptyText="暂无文档"
      renderMobileItem={(document) => (
        <div style={{ padding: 12, borderBottom: '1px solid #f0f0f0' }}>
          <Flex justify="space-between" align="center" gap={8}>
            <Text strong ellipsis>{document.source || '-'}</Text>
            {renderStatus(document)}
          </Flex>
          <Flex justify="space-between" align="center" gap={8} style={{ marginTop: 10 }}>
            <Text type="secondary">分块 {renderProgress(document)}</Text>
            <Text type="secondary" style={{ fontSize: 12 }}>
              {document.created_at
                ? new Date(document.created_at).toLocaleString('zh-CN')
                : '-'}
            </Text>
          </Flex>
        </div>
      )}
    />
  </Card>
);

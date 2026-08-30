import { DeleteOutlined, EyeOutlined, LockOutlined, SettingOutlined } from '@ant-design/icons';
import { Badge, Button, Card, Flex, Popconfirm, Progress, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import type { KnowledgeDocument } from '../model/knowledge';

import { COMPACT_PAGE_SIZE } from '@/constants';
import { RequestEditorButton } from '@/shared/components';
import { ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;

interface WorkspaceDocumentsTableProps {
  documents: KnowledgeDocument[];
  loading: boolean;
  isAdmin?: boolean;
  deletingDocumentID?: string;
  onDelete?: (documentID: string) => void;
  onPreview?: (document: KnowledgeDocument) => void;
  onSetAccess?: (document: KnowledgeDocument) => void;
  // 文档所属 workspace 名：RequestEditorButton 生成 knowledge_doc 路由的 workspaceName 段。
  workspaceName: string;
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

// 白名单任一维度非空 = 受限（admin/creator 视角）；两者全空 = 继承 workspace 可见性
const isRestricted = (doc: KnowledgeDocument) =>
  (doc.allowed_user_ids?.length ?? 0) > 0 || (doc.allowed_role_ids?.length ?? 0) > 0;

// 当前 viewer 被锁定：后端按 viewer 计算，admin/owner/creator 与平台库恒 false。
// member 受限文档元数据可发现但内容不可读，需申请查看权限。
const isLocked = (doc: KnowledgeDocument) => doc.restricted === true;

const renderName = (doc: KnowledgeDocument) => (
  <Flex align="center" gap={6} style={{ minWidth: 0 }}>
    {isLocked(doc) && <LockOutlined style={{ color: '#faad14' }} />}
    <Text ellipsis>{doc.source || '-'}</Text>
    {isRestricted(doc) && <Tag color="orange" style={{ margin: 0 }}>查看白名单受限</Tag>}
  </Flex>
);

const baseColumns: ColumnsType<KnowledgeDocument> = [
  {
    title: '文件名',
    dataIndex: 'source',
    key: 'source',
    ellipsis: true,
    render: (_, doc) => renderName(doc),
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

const deleteAction = (
  document: KnowledgeDocument,
  deletingDocumentID: string,
  onDelete: (documentID: string) => void,
) => {
  const processing = document.ingest_status === 'processing';
  const button = (
    <Button
      type="text"
      danger
      icon={<DeleteOutlined />}
      aria-label="删除文档"
      disabled={processing}
      loading={deletingDocumentID === document.id}
    />
  );
  if (processing) return <Tooltip title="处理中不可删除">{button}</Tooltip>;
  return (
    <Popconfirm
      title={`确定删除文档“${document.source || document.id}”？`}
      description="文档分块和检索向量也会一并删除。"
      okText="删除"
      cancelText="取消"
      okButtonProps={{ danger: true }}
      onConfirm={() => onDelete(document.id)}
    >
      {button}
    </Popconfirm>
  );
};

// 预览按钮：受限文档（当前 viewer 被锁定）禁用，申请查看权限后解锁。
const previewButton = (
  document: KnowledgeDocument,
  onPreview: (document: KnowledgeDocument) => void,
) => (
  isLocked(document) ? (
    <Tooltip title="已加锁，申请查看权限后解锁">
      <Button type="text" icon={<EyeOutlined />} disabled aria-label="预览文档" />
    </Tooltip>
  ) : (
    <Tooltip title="预览原文">
      <Button
        type="text"
        icon={<EyeOutlined />}
        aria-label="预览文档"
        onClick={() => onPreview(document)}
      />
    </Tooltip>
  )
);

export const WorkspaceDocumentsTable = ({
  documents,
  loading,
  isAdmin = false,
  deletingDocumentID = '',
  onDelete = () => undefined,
  onPreview,
  onSetAccess,
  workspaceName,
}: WorkspaceDocumentsTableProps) => {
  // 申请查看权限：member（admin/owner 与平台库恒 false）且当前 viewer 被锁定才显示。
  const canRequestAccess = !isAdmin;
  const actions: ColumnsType<KnowledgeDocument>[number] = {
    title: '操作',
    key: 'actions',
    width: 144,
    align: 'center' as const,
    render: (_: unknown, document: KnowledgeDocument) => (
      <Flex align="center" justify="center" gap={0}>
        {onPreview && previewButton(document, onPreview)}
        {canRequestAccess && isLocked(document) && (
          <RequestEditorButton
            resourceType="knowledge_doc"
            resourceId={document.id}
            options={{ workspaceName, resourceName: `${workspaceName}/${document.source}` }}
            buttonProps={{ type: 'link', size: 'small', style: { padding: '0 4px' } }}
          />
        )}
        {isAdmin && onSetAccess && (
          <Tooltip title="设置访问权限">
            <Button
              type="text"
              icon={<SettingOutlined />}
              aria-label="设置文档访问权限"
              onClick={() => onSetAccess(document)}
            />
          </Tooltip>
        )}
        {isAdmin && deleteAction(document, deletingDocumentID, onDelete)}
      </Flex>
    ),
  };
  const columns = isAdmin || onPreview || canRequestAccess ? [...baseColumns, actions] : baseColumns;

  return (
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
            {renderName(document)}
            {renderStatus(document)}
          </Flex>
          <Flex justify="space-between" align="center" gap={8} style={{ marginTop: 10 }}>
            <Text type="secondary">分块 {renderProgress(document)}</Text>
            <Flex align="center" gap={8}>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {document.created_at
                  ? new Date(document.created_at).toLocaleString('zh-CN')
                  : '-'}
              </Text>
              {onPreview && previewButton(document, onPreview)}
              {canRequestAccess && isLocked(document) && (
                <RequestEditorButton
                  resourceType="knowledge_doc"
                  resourceId={document.id}
                  options={{ workspaceName, resourceName: `${workspaceName}/${document.source}` }}
                  buttonProps={{ type: 'link', size: 'small', style: { padding: '0 4px' } }}
                />
              )}
              {isAdmin && onSetAccess && (
                <Button
                  type="text"
                  size="small"
                  icon={<SettingOutlined />}
                  aria-label="设置文档访问权限"
                  onClick={() => onSetAccess(document)}
                />
              )}
              {isAdmin && deleteAction(document, deletingDocumentID, onDelete)}
            </Flex>
          </Flex>
        </div>
      )}
    />
  </Card>
  );
};

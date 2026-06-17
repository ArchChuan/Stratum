import { ArrowRightOutlined, BookOutlined, DeleteOutlined } from '@ant-design/icons';
import { Button, Card, Space, Tag, Tooltip, Typography } from 'antd';

import type { Workspace } from '../model/knowledge';

import { DangerPopconfirm } from '@/shared/ui';

const { Text, Paragraph } = Typography;

const MODE_COLORS: Record<string, string> = {
  hybrid: 'purple',
  vector: 'blue',
  graph: 'green',
};
const MODE_LABELS: Record<string, string> = {
  hybrid: '混合',
  vector: '向量',
  graph: '图谱',
};

interface WorkspaceCardProps {
  ws: Workspace;
  onDelete: (name: string) => void;
  onOpen: (name: string) => void;
  isAdmin: boolean;
}

export const WorkspaceCard = ({ ws, onDelete, onOpen, isAdmin }: WorkspaceCardProps) => (
  <Card
    style={{
      borderRadius: 12,
      border: '1px solid #f0f0f0',
      height: '100%',
      display: 'flex',
      flexDirection: 'column',
      cursor: 'pointer',
    }}
    styles={{ body: { padding: 20, flex: 1, display: 'flex', flexDirection: 'column' } }}
    hoverable
    onClick={() => onOpen(ws.name)}
  >
    <div
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        justifyContent: 'space-between',
        marginBottom: 12,
      }}
    >
      <div
        style={{
          width: 40,
          height: 40,
          borderRadius: 10,
          background: '#f0f5ff',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        <BookOutlined style={{ fontSize: 18, color: '#2f54eb' }} />
      </div>
      <Tag
        color={MODE_COLORS[ws.config?.query_mode || ''] || 'default'}
        style={{ border: 'none', borderRadius: 6, fontSize: 11, fontWeight: 500 }}
      >
        {MODE_LABELS[ws.config?.query_mode || ''] || ws.config?.query_mode || '-'}
      </Tag>
    </div>

    <Text strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>
      {ws.name}
    </Text>
    <Paragraph
      type="secondary"
      ellipsis={{ rows: 2 }}
      style={{ fontSize: 13, marginBottom: 12, flex: 1, marginTop: 0 }}
    >
      {ws.description || '暂无描述'}
    </Paragraph>

    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        paddingTop: 12,
        borderTop: '1px solid #f5f5f5',
      }}
    >
      <Text type="secondary" style={{ fontSize: 12 }}>
        {ws.config?.embedding_model || '-'}
      </Text>
      <div onClick={(e) => e.stopPropagation()}>
        <Space size={4}>
          <Tooltip title="查看详情">
            <Button
              type="text"
              size="small"
              icon={<ArrowRightOutlined />}
              onClick={() => onOpen(ws.name)}
            />
          </Tooltip>
          {isAdmin && (
            <Tooltip title="删除">
              <DangerPopconfirm
                title={`确定删除知识库 "${ws.name}"？`}
                okText="删除"
                onConfirm={() => onDelete(ws.name)}
              >
                <Button type="text" size="small" danger icon={<DeleteOutlined />} />
              </DangerPopconfirm>
            </Tooltip>
          )}
        </Space>
      </div>
    </div>
  </Card>
);

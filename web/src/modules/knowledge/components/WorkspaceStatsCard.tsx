import { Card, Space, Tag, Typography } from 'antd';

import type { WorkspaceStats } from '../model/knowledge';

const { Text } = Typography;

interface WorkspaceStatsCardProps {
  stats?: WorkspaceStats;
}

export const WorkspaceStatsCard = ({ stats }: WorkspaceStatsCardProps) => (
  <Card
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
    styles={{ body: { padding: '16px 24px' } }}
  >
    <Space size={32} wrap>
      <div>
        <Text type="secondary" style={{ fontSize: 12 }}>
          嵌入模型
        </Text>
        <div>
          <Tag>{stats?.config?.embedding_model || '-'}</Tag>
        </div>
      </div>
      <div>
        <Text type="secondary" style={{ fontSize: 12 }}>
          查询模式
        </Text>
        <div>
          <Tag color="blue">{stats?.config?.query_mode || '-'}</Tag>
        </div>
      </div>
      <div>
        <Text type="secondary" style={{ fontSize: 12 }}>
          向量数
        </Text>
        <div>
          <Text strong>{stats?.stats?.row_count ?? '—'}</Text>
        </div>
      </div>
      <div>
        <Text type="secondary" style={{ fontSize: 12 }}>
          分块大小
        </Text>
        <div>
          <Text>{stats?.config?.chunk_size ?? '-'}</Text>
        </div>
      </div>
      <div>
        <Text type="secondary" style={{ fontSize: 12 }}>
          Top-K
        </Text>
        <div>
          <Text>{stats?.config?.top_k ?? '-'}</Text>
        </div>
      </div>
    </Space>
  </Card>
);

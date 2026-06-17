import { Card, Descriptions, Empty } from 'antd';

import type { MemoryStats } from '../model/memory';

interface MemoryStatsTabProps {
  stats: MemoryStats | null;
}

export const MemoryStatsTab = ({ stats }: MemoryStatsTabProps) =>
  stats ? (
    <Card size="small">
      <Descriptions column={3} bordered size="small">
        <Descriptions.Item label="总记忆数">{stats.total_entries ?? 0}</Descriptions.Item>
        <Descriptions.Item label="实体数">{stats.total_entities ?? 0}</Descriptions.Item>
        <Descriptions.Item label="关系数">{stats.total_relations ?? 0}</Descriptions.Item>
        <Descriptions.Item label="会话数">{stats.total_sessions ?? 0}</Descriptions.Item>
        <Descriptions.Item label="向量数">{stats.vector_count ?? 0}</Descriptions.Item>
        <Descriptions.Item label="富化数">{stats.enriched_count ?? 0}</Descriptions.Item>
      </Descriptions>
    </Card>
  ) : (
    <Empty description="暂无统计数据" />
  );

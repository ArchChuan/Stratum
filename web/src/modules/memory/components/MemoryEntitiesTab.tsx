import { ReloadOutlined } from '@ant-design/icons';
import { Button, Empty, Space, Tag } from 'antd';

import type { MemoryEntity } from '../model/memory';

interface MemoryEntitiesTabProps {
  entities: MemoryEntity[];
  onLoad: () => void;
}

export const MemoryEntitiesTab = ({ entities, onLoad }: MemoryEntitiesTabProps) => (
  <Space direction="vertical" style={{ width: '100%' }}>
    <Button icon={<ReloadOutlined />} onClick={onLoad}>
      加载实体
    </Button>
    {entities.length > 0 ? (
      <Space wrap>
        {entities.map((e) => (
          <Tag key={e.name} color="blue">
            {e.name} ({e.type})
          </Tag>
        ))}
      </Space>
    ) : (
      <Empty description="点击加载实体" />
    )}
  </Space>
);

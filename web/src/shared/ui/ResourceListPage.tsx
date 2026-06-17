import { PlusOutlined } from '@ant-design/icons';
import { Button, Typography } from 'antd';
import type { ReactNode } from 'react';

import { EmptyHint } from './EmptyHint';
import { ListSkeleton } from './ListSkeleton';

const { Title, Text } = Typography;

export interface ResourceListPageProps<T> {
  title: string;
  description?: string;
  loading: boolean;
  items: T[];
  renderItem: (item: T, index: number) => ReactNode;
  createLabel?: string;
  onCreate?: () => void;
  emptyTitle?: string;
  emptyDescription?: string;
  toolbarExtra?: ReactNode;
}

export function ResourceListPage<T>({
  title,
  description,
  loading,
  items,
  renderItem,
  createLabel = '新建',
  onCreate,
  emptyTitle = '暂无数据',
  emptyDescription,
  toolbarExtra,
}: ResourceListPageProps<T>) {
  return (
    <div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 16,
        }}
      >
        <div>
          <Title level={4} style={{ margin: 0 }}>
            {title}
          </Title>
          {description && (
            <Text type="secondary" style={{ fontSize: 13 }}>
              {description}
            </Text>
          )}
        </div>
        <div>
          {toolbarExtra}
          {onCreate && (
            <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>
              {createLabel}
            </Button>
          )}
        </div>
      </div>

      {loading ? (
        <ListSkeleton count={3} />
      ) : items.length === 0 ? (
        <EmptyHint title={emptyTitle} description={emptyDescription} />
      ) : (
        <>{items.map(renderItem)}</>
      )}
    </div>
  );
}

export default ResourceListPage;

import { Card, Skeleton } from 'antd';

export const WorkspaceDetailSkeleton = () => (
  <div>
    <Skeleton active paragraph={{ rows: 1 }} style={{ marginBottom: 20 }} />
    <Card style={{ borderRadius: 12, marginBottom: 16 }}>
      <Skeleton active />
    </Card>
    <Card style={{ borderRadius: 12 }}>
      <Skeleton active />
    </Card>
  </div>
);

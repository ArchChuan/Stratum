import { Card, Col, Row, Skeleton } from 'antd';

export const WorkspaceListSkeleton = () => (
  <Row gutter={[16, 16]}>
    {[1, 2, 3].map((i) => (
      <Col xs={24} sm={12} lg={8} key={i}>
        <Card
          style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}
          styles={{ body: { padding: 20 } }}
        >
          <Skeleton active avatar paragraph={{ rows: 2 }} />
        </Card>
      </Col>
    ))}
  </Row>
);

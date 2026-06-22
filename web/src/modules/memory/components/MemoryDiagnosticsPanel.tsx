import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, Row, Skeleton, Statistic, Typography } from 'antd';

import { FrecencyChart } from './FrecencyChart';
import { TopEntitiesTable } from './TopEntitiesTable';
import { useMemoryDiagnostics } from '../hooks/useMemoryDiagnostics';

const { Title } = Typography;

interface MemoryDiagnosticsPanelProps {
  tenantId: string;
}

export const MemoryDiagnosticsPanel = ({ tenantId }: MemoryDiagnosticsPanelProps) => {
  const { diagnostics, loading, error, refetch } = useMemoryDiagnostics(tenantId);

  if (error) {
    return <Alert message="加载失败" description={error} type="error" showIcon />;
  }

  if (loading && !diagnostics) {
    return <Skeleton active paragraph={{ rows: 8 }} />;
  }

  if (!diagnostics) {
    return null;
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}>
          内存统计
        </Title>
        <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={loading}>
          刷新
        </Button>
      </div>

      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col span={6}>
          <Card>
            <Statistic title="事实数" value={diagnostics.total_facts} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="实体数" value={diagnostics.total_entities} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="关系数" value={diagnostics.total_relationships} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="平均 Frecency"
              value={diagnostics.avg_frecency}
              precision={3}
            />
          </Card>
        </Col>
      </Row>

      <Card title="Frecency 分布" style={{ marginBottom: 24 }}>
        <FrecencyChart data={diagnostics.frecency_distribution} />
      </Card>

      <Card title="热门实体 (Top 10)" style={{ marginBottom: 24 }}>
        <TopEntitiesTable entities={diagnostics.top_entities} loading={loading} />
      </Card>

      <Card title="管道状态">
        <Row gutter={16}>
          <Col span={8}>
            <Statistic title="队列深度" value={diagnostics.pipeline_status.queue_depth} />
          </Col>
          <Col span={8}>
            <Statistic
              title="处理速率"
              value={diagnostics.pipeline_status.processing_rate_per_min ?? 0}
              suffix="条/分钟"
            />
          </Col>
          <Col span={8}>
            <Statistic
              title="最后处理时间"
              value={
                diagnostics.pipeline_status.last_processed_at
                  ? new Date(diagnostics.pipeline_status.last_processed_at).toLocaleString('zh-CN')
                  : '未处理'
              }
            />
          </Col>
        </Row>
      </Card>
    </div>
  );
};

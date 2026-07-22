import {
  AppstoreOutlined,
  RobotOutlined,
  ApiOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import { Col, Row, Typography } from 'antd';
import type { ReactNode } from 'react';

import { StatCard } from '../components/StatCard';
import { useDashboardPage } from '../hooks/useDashboardPage';

const { Title, Text } = Typography;

interface StatCardSpec {
  title: string;
  value: number;
  icon: ReactNode;
  color: string;
  bg: string;
}

export const DashboardPage = () => {
  const { counts, loading } = useDashboardPage();

  const statCards: StatCardSpec[] = [
    { title: 'Agent', value: counts.agents, icon: <RobotOutlined />, color: '#1677ff', bg: '#e6f4ff' },
    { title: '技能', value: counts.skills, icon: <AppstoreOutlined />, color: '#52c41a', bg: '#f6ffed' },
    { title: '知识库', value: counts.knowledge, icon: <DatabaseOutlined />, color: '#13c2c2', bg: '#e6fffb' },
    { title: 'MCP 服务器', value: counts.mcpServers, icon: <ApiOutlined />, color: '#722ed1', bg: '#f9f0ff' },
  ];

  return (
    <div>
      <div style={{ marginBottom: 24 }}>
        <Title level={4} style={{ margin: 0 }}>
          概览
        </Title>
        <Text type="secondary" style={{ fontSize: 13 }}>
          系统运行状态一览
        </Text>
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        {statCards.map((s) => (
          <Col xs={24} sm={12} lg={8} xl={4} key={s.title} style={{ flex: '1 1 0' }}>
            <StatCard {...s} loading={loading} />
          </Col>
        ))}
      </Row>
    </div>
  );
};

export default DashboardPage;

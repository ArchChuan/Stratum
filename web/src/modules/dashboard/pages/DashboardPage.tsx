import {
  AppstoreOutlined,
  RobotOutlined,
  ApiOutlined,
  DatabaseOutlined,
  CloudServerOutlined,
  TeamOutlined,
  ApartmentOutlined,
  MessageOutlined,
} from '@ant-design/icons';
import { Col, Row, Typography } from 'antd';
import type { ReactNode } from 'react';

import { RecentExecutionsTable } from '../components/RecentExecutionsTable';
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
  const { counts, loading, executions, executionsTotal, executionsLoading, page, pageSize, handlePageChange } =
    useDashboardPage();

  const statCards: StatCardSpec[] = [
    { title: 'Agent', value: counts.agents, icon: <RobotOutlined />, color: '#1677ff', bg: '#e6f4ff' },
    { title: '技能', value: counts.skills, icon: <AppstoreOutlined />, color: '#52c41a', bg: '#f6ffed' },
    { title: '知识库', value: counts.knowledge_workspaces, icon: <DatabaseOutlined />, color: '#13c2c2', bg: '#e6fffb' },
    { title: 'MCP 服务器', value: counts.mcp_servers, icon: <ApiOutlined />, color: '#722ed1', bg: '#f9f0ff' },
    { title: '模型厂商', value: counts.model_providers, icon: <CloudServerOutlined />, color: '#d46b08', bg: '#fff7e6' },
    { title: '租户成员', value: counts.tenant_members, icon: <TeamOutlined />, color: '#08979c', bg: '#e6fffb' },
    { title: '工作流', value: counts.workflows, icon: <ApartmentOutlined />, color: '#389e0d', bg: '#f6ffed' },
    { title: '近七日 Agent 对话', value: counts.agent_user_messages_7d, icon: <MessageOutlined />, color: '#c41d7f', bg: '#fff0f6' },
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
          <Col xs={24} sm={12} lg={6} key={s.title}>
            <StatCard {...s} loading={loading} />
          </Col>
        ))}
      </Row>

      <div style={{ marginBottom: 16 }}>
        <Title level={5} style={{ margin: 0 }}>
          最近执行
        </Title>
        <Text type="secondary" style={{ fontSize: 13 }}>
          当前用户的 Agent 执行记录
        </Text>
      </div>
      <RecentExecutionsTable
        data={executions}
        loading={loading || executionsLoading}
        total={executionsTotal}
        page={page}
        pageSize={pageSize}
        onPageChange={handlePageChange}
      />
    </div>
  );
};

export default DashboardPage;

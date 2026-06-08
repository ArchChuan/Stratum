import React, { useState, useEffect } from 'react';
import { Card, Row, Col, Statistic, Table, Typography, Tag, message } from 'antd';
import {
  AppstoreOutlined,
  RobotOutlined,
  ApiOutlined,
  ThunderboltOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import { getAllSkills, getAllAgents, getAgentExecutions, getKnowledgeWorkspaces } from '../services/api';
import api from '../services/api';

const { Title } = Typography;

const statusColor = { success: 'green', error: 'red' };

const execColumns = [
  {
    title: 'Agent',
    dataIndex: 'agent_name',
    key: 'agent_name',
    ellipsis: true,
  },
  {
    title: '状态',
    dataIndex: 'status',
    key: 'status',
    width: 70,
    render: (s) => <Tag color={statusColor[s] || 'default'}>{s === 'success' ? '成功' : '失败'}</Tag>,
  },
  {
    title: '输入',
    dataIndex: 'input_preview',
    key: 'input_preview',
    ellipsis: true,
  },
  {
    title: 'Token',
    dataIndex: 'total_tokens',
    key: 'total_tokens',
    width: 80,
  },
  {
    title: '时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 160,
    render: (d) => new Date(d).toLocaleString('zh-CN'),
  },
];

const DashboardPage = () => {
  const [counts, setCounts] = useState({ skills: 0, agents: 0, mcpServers: 0, executions: 0, knowledge: 0 });
  const [recentExecs, setRecentExecs] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;

    const fetchAll = async () => {
      setLoading(true);
      try {
        const [skillsRes, agentsRes, execsRes, mcpRes, knowledgeRes] = await Promise.allSettled([
          getAllSkills(),
          getAllAgents(),
          getAgentExecutions(),
          api.get('/api/v1/mcp/servers'),
          getKnowledgeWorkspaces(),
        ]);

        if (cancelled) return;

        const skills = skillsRes.status === 'fulfilled' ? (skillsRes.value.data || []) : [];
        const agents = agentsRes.status === 'fulfilled' ? (agentsRes.value.data?.agents || []) : [];
        const execs  = execsRes.status  === 'fulfilled' ? (execsRes.value.data?.executions || []) : [];
        const mcpServers = mcpRes.status === 'fulfilled' ? (mcpRes.value.data?.servers || []) : [];
        const workspaces = knowledgeRes.status === 'fulfilled' ? (knowledgeRes.value.data?.workspaces || knowledgeRes.value.data || []) : [];

        setCounts({
          skills: skills.length,
          agents: agents.length,
          mcpServers: mcpServers.length,
          executions: execs.length,
          knowledge: workspaces.length,
        });
        setRecentExecs(execs.slice(0, 8));
      } catch {
        if (!cancelled) message.error('加载仪表盘数据失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchAll();
    return () => { cancelled = true; };
  }, []);

  const statCards = [
    { title: 'Agent 数量', value: counts.agents, icon: <RobotOutlined />, color: '#1677ff' },
    { title: '技能数量', value: counts.skills, icon: <AppstoreOutlined />, color: '#52c41a' },
    { title: '知识库', value: counts.knowledge, icon: <DatabaseOutlined />, color: '#13c2c2' },
    { title: 'MCP 服务器', value: counts.mcpServers, icon: <ApiOutlined />, color: '#722ed1' },
    { title: '近30天执行', value: counts.executions, icon: <ThunderboltOutlined />, color: '#fa8c16' },
  ];

  return (
    <div>
      <Row gutter={16}>
        {statCards.map((s) => (
          <Col flex="20%" key={s.title}>
            <Card loading={loading}>
              <Statistic
                title={s.title}
                value={s.value}
                prefix={<span style={{ color: s.color, marginRight: 4 }}>{s.icon}</span>}
                valueStyle={{ color: s.color }}
              />
            </Card>
          </Col>
        ))}
      </Row>

      <div style={{ marginTop: 24 }}>
        <Title level={4} style={{ marginBottom: 12 }}>最近执行记录</Title>
        <Card>
          <Table
            dataSource={recentExecs}
            columns={execColumns}
            rowKey="id"
            loading={loading}
            pagination={false}
            locale={{ emptyText: '暂无执行记录' }}
            size="small"
          />
        </Card>
      </div>
    </div>
  );
};

export default DashboardPage;

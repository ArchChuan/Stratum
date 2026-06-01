import React, { useState, useEffect } from 'react';
import { Layout, Menu, Space, Typography } from 'antd';
import {
  AppstoreOutlined,
  PlusCircleOutlined,
  HistoryOutlined,
  DashboardOutlined,
  RobotOutlined,
  CommentOutlined,
  DatabaseOutlined
} from '@ant-design/icons';
import { Routes, Route, useNavigate, Link } from 'react-router-dom';

import SkillsListPage from './pages/SkillsListPage';
import CreateSkillPage from './pages/CreateSkillPage';
import ExecutionHistoryPage from './pages/ExecutionHistoryPage';
import DashboardPage from './pages/DashboardPage';
import AgentsListPage from './pages/AgentsListPage';
import CreateAgentPage from './pages/CreateAgentPage';
import AgentChatPage from './pages/AgentChatPage';
import MemoryPage from './pages/MemoryPage';
import { checkHealth } from './services/api';

const { Header, Content, Sider } = Layout;
const { Title } = Typography;

const App = () => {
  const [collapsed, setCollapsed] = useState(false);
  const [connected, setConnected] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    // 检查后端连接状态
    const checkConnection = async () => {
      try {
        await checkHealth();
        setConnected(true);
      } catch (error) {
        console.error('Backend connection failed:', error);
        setConnected(false);
      }
    };

    checkConnection();
  }, []);

  const menuItems = [
    {
      key: '/',
      icon: <DashboardOutlined />,
      label: <Link to="/">仪表盘</Link>,
    },
    {
      key: '/skills',
      icon: <AppstoreOutlined />,
      label: <Link to="/skills">技能管理</Link>,
    },
    {
      key: '/skills/create',
      icon: <PlusCircleOutlined />,
      label: <Link to="/skills/create">创建技能</Link>,
    },
    {
      key: '/agents',
      icon: <RobotOutlined />,
      label: <Link to="/agents">智能代理</Link>,
    },
    {
      key: '/agents/create',
      icon: <PlusCircleOutlined />,
      label: <Link to="/agents/create">创建代理</Link>,
    },
    {
      key: '/chat',
      icon: <CommentOutlined />,
      label: <Link to="/chat">代理对话</Link>,
    },
    {
      key: '/memory',
      icon: <DatabaseOutlined />,
      label: <Link to="/memory">记忆管理</Link>,
    },
    {
      key: '/history',
      icon: <HistoryOutlined />,
      label: <Link to="/history">执行历史</Link>,
    },
  ];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider 
        collapsible 
        collapsed={collapsed} 
        onCollapse={(value) => setCollapsed(value)}
        style={{ 
          overflow: 'auto', 
          height: '100vh', 
          position: 'fixed', 
          left: 0, 
          top: 0, 
          bottom: 0 
        }}
      >
        <div style={{ padding: '16px', textAlign: 'center' }}>
          <Title style={{ color: 'white', fontSize: '16px' }} ellipsis>
            ClawHermes AI Frontend
          </Title>
        </div>
        <Menu 
          theme="dark" 
          defaultSelectedKeys={['/']} 
          mode="inline" 
          items={menuItems}
          onClick={({ key }) => navigate(key)}
        />
      </Sider>

      <Layout style={{ marginLeft: collapsed ? 80 : 200 }}>
        <Header style={{ 
          padding: 0, 
          background: '#fff', 
          display: 'flex', 
          alignItems: 'center',
          justifyContent: 'space-between',
          paddingLeft: '16px'
        }}>
          <Space>
            <span>状态:</span>
            <span style={{ color: connected ? 'green' : 'red' }}>
              {connected ? '已连接' : '未连接'}
            </span>
          </Space>
        </Header>

        <Content style={{ margin: '24px 16px 0', overflow: 'initial' }}>
          <div style={{ padding: 24, background: '#fff', minHeight: 360 }}>
            <Routes>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/skills" element={<SkillsListPage />} />
              <Route path="/skills/create" element={<CreateSkillPage />} />
              <Route path="/agents" element={<AgentsListPage />} />
              <Route path="/agents/create" element={<CreateAgentPage />} />
              <Route path="/chat" element={<AgentChatPage />} />
              <Route path="/memory" element={<MemoryPage />} />
              <Route path="/history" element={<ExecutionHistoryPage />} />
            </Routes>
          </div>
        </Content>
      </Layout>
    </Layout>
  );
};

export default App;
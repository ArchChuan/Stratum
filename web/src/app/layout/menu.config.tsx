import {
  AppstoreOutlined,
  PlusCircleOutlined,
  HistoryOutlined,
  DashboardOutlined,
  RobotOutlined,
  CommentOutlined,
  DatabaseOutlined,
  TeamOutlined,
  SettingOutlined,
  GlobalOutlined,
  ApiOutlined,
  BookOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import type { MenuProps } from 'antd';
import { Link } from 'react-router-dom';

import type { User } from '@/modules/iam';

type MenuItem = NonNullable<MenuProps['items']>[number];

export const buildMenuItems = (user: User | null | undefined): MenuItem[] => {
  const base: MenuItem[] = [
    { key: '/', icon: <DashboardOutlined />, label: <Link to="/">概览</Link> },
    {
      key: 'agent-group',
      icon: <RobotOutlined />,
      label: 'Agent',
      children: [
        { key: '/agents', icon: <RobotOutlined />, label: <Link to="/agents">Agent 列表</Link> },
        {
          key: '/agents/create',
          icon: <PlusCircleOutlined />,
          label: <Link to="/agents/create">创建 Agent</Link>,
        },
        { key: '/chat', icon: <CommentOutlined />, label: <Link to="/chat">Agent 对话</Link> },
        { key: '/history', icon: <HistoryOutlined />, label: <Link to="/history">执行历史</Link> },
      ],
    },
    {
      key: 'skill-group',
      icon: <ThunderboltOutlined />,
      label: '技能',
      children: [
        { key: '/skills', icon: <AppstoreOutlined />, label: <Link to="/skills">技能列表</Link> },
        {
          key: '/skills/create',
          icon: <PlusCircleOutlined />,
          label: <Link to="/skills/create">创建技能</Link>,
        },
      ],
    },
    {
      key: 'knowledge-group',
      icon: <BookOutlined />,
      label: '知识与记忆',
      children: [
        { key: '/knowledge', icon: <BookOutlined />, label: <Link to="/knowledge">知识库</Link> },
        { key: '/memory', icon: <DatabaseOutlined />, label: <Link to="/memory">记忆管理</Link> },
      ],
    },
    {
      key: 'mcp-group',
      icon: <ApiOutlined />,
      label: 'MCP 服务器',
      children: [
        { key: '/mcp', icon: <ApiOutlined />, label: <Link to="/mcp">服务器列表</Link> },
        {
          key: '/mcp/create',
          icon: <PlusCircleOutlined />,
          label: <Link to="/mcp/create">添加服务器</Link>,
        },
      ],
    },
  ];

  if (user?.current_tenant) {
    base.push({
      key: 'tenant-group',
      icon: <TeamOutlined />,
      label: '团队',
      children: [
        {
          key: '/tenant/members',
          icon: <TeamOutlined />,
          label: <Link to="/tenant/members">成员管理</Link>,
        },
        {
          key: '/tenant/settings',
          icon: <SettingOutlined />,
          label: <Link to="/tenant/settings">租户设置</Link>,
        },
      ],
    });
  }

  if (user?.global_role === 'global_admin') {
    base.push({
      key: '/admin/tenants',
      icon: <GlobalOutlined />,
      label: <Link to="/admin/tenants">全局租户</Link>,
    });
  }

  return base;
};

export const resolveOpenKeys = (pathname: string): string[] => {
  if (['/agents', '/chat', '/history'].some((p) => pathname.startsWith(p))) return ['agent-group'];
  if (pathname.startsWith('/skills')) return ['skill-group'];
  if (['/knowledge', '/memory'].some((p) => pathname.startsWith(p))) return ['knowledge-group'];
  if (pathname.startsWith('/mcp')) return ['mcp-group'];
  if (pathname.startsWith('/tenant')) return ['tenant-group'];
  return [];
};

import {
  AppstoreOutlined,
  PlusCircleOutlined,
  DashboardOutlined,
  RobotOutlined,
  CommentOutlined,
  TeamOutlined,
  SettingOutlined,
  GlobalOutlined,
  ApiOutlined,
  BookOutlined,
  ThunderboltOutlined,
  ExperimentOutlined,
  BranchesOutlined,
  HistoryOutlined,
  ScheduleOutlined,
  FileTextOutlined,
  AuditOutlined,
  DatabaseOutlined,
} from '@ant-design/icons';
import type { MenuProps } from 'antd';

import type { User } from '@/modules/iam';

type MenuItem = NonNullable<MenuProps['items']>[number];

/**
 * label 一律用字符串,不用 <Link> ReactNode。
 * 根因(实测):antd Menu 每次路由切换对 26 个 <Link> 全量 reconcile,
 * 主线程阻塞 50-80ms,合成器保留旧帧产生残影/慢。
 * 字符串 label 后 reconcile 降至 ~0ms;导航由 key + AppShell 的 onClick 承担。
 */
export const buildMenuItems = (user: User | null | undefined): MenuItem[] => {
  const tenantRole = user?.role ?? user?.current_tenant?.role ?? 'member';
  const canManageTenant = tenantRole === 'admin' || tenantRole === 'owner';
  const base: MenuItem[] = [
    { key: '/', icon: <DashboardOutlined />, label: '概览' },
    { key: '/chat', icon: <CommentOutlined />, label: 'Agent 对话' },
    {
      key: 'workflow-group',
      icon: <BranchesOutlined />,
      label: '流程',
      children: [
        { key: '/workflows', icon: <BranchesOutlined />, label: '工作流' },
        { key: '/workflow-runs', icon: <HistoryOutlined />, label: '运行中心' },
        { key: '/scheduled-tasks', icon: <ScheduleOutlined />, label: '定时任务' },
        canManageTenant ? {
          key: '/workflows/new', icon: <PlusCircleOutlined />, label: '新建工作流',
        } : null,
      ],
    },
    {
      key: 'agent-group',
      icon: <RobotOutlined />,
      label: 'Agent',
      children: [
        { key: '/agents', icon: <RobotOutlined />, label: 'Agent 管理' },
        canManageTenant ? {
          key: '/agents/create',
          icon: <PlusCircleOutlined />,
          label: '创建 Agent',
        } : null,
      ],
    },
    {
      key: 'skill-group',
      icon: <ThunderboltOutlined />,
      label: '技能',
      children: [
        { key: '/skills', icon: <AppstoreOutlined />, label: '技能列表' },
        canManageTenant ? {
          key: '/skills/create',
          icon: <PlusCircleOutlined />,
          label: '创建技能',
        } : null,
      ],
    },
    {
      key: 'evaluation-group',
      icon: <ExperimentOutlined />,
      label: '评测',
      children: [
        { key: '/evaluations', icon: <ExperimentOutlined />, label: '评测与进化' },
      ],
    },
    {
      key: '/knowledge',
      icon: <BookOutlined />,
      label: '知识库',
    },
    {
      key: '/memory',
      icon: <DatabaseOutlined />,
      label: '我的记忆',
    },
    {
      key: 'mcp-group',
      icon: <ApiOutlined />,
      label: 'MCP 服务器',
      children: [
        { key: '/mcp', icon: <ApiOutlined />, label: '服务器列表' },
        canManageTenant ? {
          key: '/mcp/create',
          icon: <PlusCircleOutlined />,
          label: '添加服务器',
        } : null,
      ],
    },
  ];

  base.push({
    key: 'model-group',
    icon: <ApiOutlined />,
    label: '模型管理',
    children: [
      { key: '/models', icon: <SettingOutlined />, label: '模型管理' },
    ],
  });

  if (user?.current_tenant) {
    base.push({
      key: 'tenant-group',
      icon: <TeamOutlined />,
      label: '团队',
      children: [
        {
          key: '/tenant/members',
          icon: <TeamOutlined />,
          label: '成员管理',
        },
        {
          key: '/tenant/settings',
          icon: <SettingOutlined />,
          label: '租户设置',
        },
      ],
    });
  }

  if (canManageTenant) {
    base.push({
      key: 'tenant-admin-group',
      icon: <SettingOutlined />,
      label: '平台管理',
      children: [
        {
          key: '/prompts',
          icon: <FileTextOutlined />,
          label: '提示词管理',
        },
        {
          key: '/audit',
          icon: <AuditOutlined />,
          label: '审计日志',
        },
      ],
    });
  }

  if (user?.global_role === 'global_admin' || user?.system_role === 'system_admin') {
    const adminItems: MenuItem[] = [];

    if (user?.global_role === 'global_admin') {
      adminItems.push({
        key: '/admin/tenants',
        icon: <GlobalOutlined />,
        label: '全局租户',
      });
      adminItems.push({
        key: '/admin/settings',
        icon: <SettingOutlined />,
        label: '平台参数',
      });
    }

    if (adminItems.length > 0) {
      base.push({
        key: 'admin-group',
        icon: <SettingOutlined />,
        label: '系统管理',
        children: adminItems,
      });
    }
  }

  return base;
};

export const resolveOpenKeys = (pathname: string): string[] => {
  if (pathname.startsWith('/agents')) return ['agent-group'];
  if (pathname.startsWith('/skills')) return ['skill-group'];
  if (pathname.startsWith('/mcp')) return ['mcp-group'];
  if (pathname.startsWith('/models')) return ['model-group'];
  if (pathname.startsWith('/evaluations')) return ['evaluation-group'];
  if (pathname.startsWith('/workflows') || pathname.startsWith('/workflow-runs') || pathname.startsWith('/scheduled-tasks')) return ['workflow-group'];
  if (pathname.startsWith('/tenant')) return ['tenant-group'];
  if (pathname.startsWith('/prompts') || pathname.startsWith('/audit')) return ['tenant-admin-group'];
  if (pathname.startsWith('/admin')) return ['admin-group'];
  return [];
};

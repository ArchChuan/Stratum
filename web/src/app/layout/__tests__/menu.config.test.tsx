import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { describe, expect, it } from 'vitest';

import { buildMenuItems, resolveOpenKeys } from '../menu.config';

const collectLabels = (items: ReturnType<typeof buildMenuItems>): ReactNode[] =>
  items.flatMap((item) => {
    if (!item || typeof item !== 'object') return [];
    const current = 'label' in item ? [item.label] : [];
    const children = 'children' in item && Array.isArray(item.children)
      ? collectLabels(item.children)
      : [];
    return [...current, ...children];
  });

/**
 * label 是纯字符串(导航由 key + AppShell 的 onClick 承担,不再是 <Link>)。
 * 测试断言菜单项文本与权限过滤;href 语义由 E2E 覆盖。
 */
describe('buildMenuItems', () => {
  it('hides tenant management routes from members', () => {
    const labels = collectLabels(buildMenuItems({
          sub: 'user-1',
          tenant_id: 'tenant-1',
          role: 'member',
          avatar_url: '',
          github_login: 'member',
          username: '',
          current_tenant: { id: 'tenant-1', name: 'Test', role: 'member' },
        }));
    render(<div>{labels.map((label, index) => <div key={index}>{label}</div>)}</div>);

    expect(screen.getByText('Agent 管理')).toBeInTheDocument();
    expect(screen.getByText('技能列表')).toBeInTheDocument();
    expect(screen.getByText('服务器列表')).toBeInTheDocument();
    expect(screen.getByText('工作流')).toBeInTheDocument();
    expect(screen.queryByText('新建工作流')).not.toBeInTheDocument();
    expect(screen.queryByText('创建 Agent')).not.toBeInTheDocument();
    expect(screen.queryByText('创建技能')).not.toBeInTheDocument();
    expect(screen.queryByText('添加服务器')).not.toBeInTheDocument();
    expect(screen.getByText('评测与进化')).toBeInTheDocument();
  });

  it('opens the evaluation navigation group', () => {
    expect(resolveOpenKeys('/evaluations')).toEqual(['evaluation-group']);
  });

  it('shows workflow authoring only to tenant admins', () => {
    const labels = collectLabels(buildMenuItems({
      sub: 'admin-1', tenant_id: 'tenant-1', role: 'admin', avatar_url: '', github_login: 'admin', username: '',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'admin' },
    }));
    render(<div>{labels.map((label, index) => <div key={index}>{label}</div>)}</div>);
    expect(screen.getByText('工作流')).toBeInTheDocument();
    expect(screen.getByText('新建工作流')).toBeInTheDocument();
    expect(resolveOpenKeys('/workflows/new')).toEqual(['workflow-group']);
  });

  it('hides the platform admin group from tenant admins (no global_role)', () => {
    const adminLabels = collectLabels(buildMenuItems({
      sub: 'admin-1', tenant_id: 'tenant-1', role: 'admin', avatar_url: '', github_login: 'admin', username: '',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'admin' },
    }));
    render(<div>{adminLabels.map((label, index) => <div key={index}>{label}</div>)}</div>);
    // 平台管理面仅 global admin 可见:租户 admin 看不到合并后的组及其子项
    expect(screen.queryByText('平台管理')).not.toBeInTheDocument();
    expect(screen.queryByText('审计日志')).not.toBeInTheDocument();
    expect(screen.queryByText('模型档案')).not.toBeInTheDocument();
    // 工具审批是独立菜单行,租户 admin 保留可见
    expect(screen.getByText('工具审批')).toBeInTheDocument();
  });

  it('hides the platform admin group from members', () => {
    const memberLabels = collectLabels(buildMenuItems({
      sub: 'user-1', tenant_id: 'tenant-1', role: 'member', avatar_url: '', github_login: 'member', username: '',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'member' },
    }));
    render(<div>{memberLabels.map((label, index) => <div key={index}>{label}</div>)}</div>);
    expect(screen.queryByText('平台管理')).not.toBeInTheDocument();
    expect(screen.queryByText('模型管理')).not.toBeInTheDocument();
    expect(screen.queryByText('审计日志')).not.toBeInTheDocument();
    expect(screen.queryByText('模型档案')).not.toBeInTheDocument();
    expect(screen.queryByText('全局租户')).not.toBeInTheDocument();
    expect(screen.queryByText('平台参数')).not.toBeInTheDocument();
    // 工具审批同样对成员隐藏
    expect(screen.queryByText('工具审批')).not.toBeInTheDocument();
  });

  it('shows the merged platform admin group only to global admin', () => {
    const labels = collectLabels(buildMenuItems({
      sub: 'ga-1', tenant_id: 'tenant-1', role: 'member', global_role: 'global_admin',
      avatar_url: '', github_login: 'ga', username: '',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'member' },
    }));
    render(<div>{labels.map((label, index) => <div key={index}>{label}</div>)}</div>);
    expect(screen.getByText('平台管理')).toBeInTheDocument();
    expect(screen.getByText('模型管理')).toBeInTheDocument();
    expect(screen.getByText('审计日志')).toBeInTheDocument();
    expect(screen.getByText('模型档案')).toBeInTheDocument();
    expect(screen.getByText('全局租户')).toBeInTheDocument();
    expect(screen.getByText('平台参数')).toBeInTheDocument();
  });

  it('shows 工具审批 to tenant admins even when not global admin', () => {
    const labels = collectLabels(buildMenuItems({
      sub: 'owner-1', tenant_id: 'tenant-1', role: 'owner', avatar_url: '', github_login: 'owner', username: '',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'owner' },
    }));
    render(<div>{labels.map((label, index) => <div key={index}>{label}</div>)}</div>);
    expect(screen.getByText('工具审批')).toBeInTheDocument();
    expect(screen.queryByText('平台管理')).not.toBeInTheDocument();
  });

  it('resolves platform admin paths to the merged open-key group', () => {
    expect(resolveOpenKeys('/models')).toEqual(['platform-admin-group']);
    expect(resolveOpenKeys('/audit')).toEqual(['platform-admin-group']);
    expect(resolveOpenKeys('/mechanism/profiles')).toEqual(['platform-admin-group']);
    expect(resolveOpenKeys('/admin/tenants')).toEqual(['platform-admin-group']);
    expect(resolveOpenKeys('/admin/settings')).toEqual(['platform-admin-group']);
    // 工具审批是独立菜单项,不再归入任何分组
    expect(resolveOpenKeys('/approvals')).toEqual([]);
  });

  it('does not expose execution history in navigation', () => {
    const items = buildMenuItems({
      sub: 'user-1',
      tenant_id: 'tenant-1',
      role: 'member',
      avatar_url: '',
      github_login: 'member',
      username: '',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'member' },
    });

    const labels = collectLabels(items);
    render(<div>{labels.map((label, index) => <div key={index}>{label}</div>)}</div>);

    expect(screen.queryByText('执行历史')).not.toBeInTheDocument();
    expect(items.some((item) => item && 'key' in item && item.key === '/history')).toBe(false);
    expect(resolveOpenKeys('/history')).toEqual([]);
    expect(resolveOpenKeys('/agents')).toEqual(['agent-group']);
  });
});

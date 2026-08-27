import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeAll, beforeEach, expect, it, vi } from 'vitest';

import { AdminsPage } from '../AdminsPage';

const api = vi.hoisted(() => ({
  listAdmins: vi.fn(),
  removeAdminRole: vi.fn(),
  searchAdminCandidates: vi.fn(),
  setAdminRole: vi.fn(),
}));

vi.mock('@/shared/hooks', () => ({ useResponsive: () => ({ isMobile: true }) }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => ({}) }));
vi.mock('@/modules/iam/api/tenant.api', () => ({ tenantApi: api }));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

beforeEach(() => {
  vi.clearAllMocks();
});

it('renders platform admins with role tags and disables removal for super admins', async () => {
  // tenantApi.listAdmins 返回裸 AdminUser[]（后端 GET /admin/admins 的 admins 数组）
  api.listAdmins.mockResolvedValue([
    { user_id: 'u-admin', username: '林管理员', github_login: 'linadmin', avatar_url: '', global_role: 'system_admin' },
    { user_id: 'u-super', username: '超级用户', github_login: 'super', avatar_url: '', global_role: 'global_admin' },
  ]);
  render(<AdminsPage />);

  expect(await screen.findByText('林管理员')).toBeInTheDocument();
  // 页面标题 h4 与 system_admin 角色 Tag 都渲染「平台管理员」
  expect(screen.getAllByText('平台管理员')).toHaveLength(2);
  expect(screen.getByText('超级管理员')).toBeInTheDocument();

  // antd 对双 CJK 字符按钮自动插空格（「移 除」），accessible name 用 \s* 兼容
  const removeButtons = screen.getAllByRole('button', { name: /移\s*除/ });
  expect(removeButtons).toHaveLength(2);
  expect(removeButtons[0]).not.toBeDisabled();
  expect(removeButtons[1]).toBeDisabled();
});

it('removes a platform admin after confirmation and refreshes the list', async () => {
  api.listAdmins.mockResolvedValue([
    { user_id: 'u-admin', username: '林管理员', github_login: '', avatar_url: '', global_role: 'system_admin' },
  ]);
  api.removeAdminRole.mockResolvedValue(undefined);
  render(<AdminsPage />);

  fireEvent.click(await screen.findByRole('button', { name: /移\s*除/ }));
  // 标题「确认移除「...」…」与 ok 按钮都含「确认移除」，用「确认移除「」仅命中标题
  const confirmation = await screen.findByText(/确认移除「/);
  const popover = confirmation.closest('.ant-popover');
  fireEvent.click(within(popover as HTMLElement).getByRole('button', { name: '确认移除' }));

  await waitFor(() => expect(api.removeAdminRole).toHaveBeenCalledWith('u-admin'));
  await waitFor(() => expect(api.listAdmins).toHaveBeenCalledTimes(2));
});

it('searches candidate users and adds a platform admin', async () => {
  api.listAdmins.mockResolvedValue([]);
  api.searchAdminCandidates.mockResolvedValue([
    { user_id: 'u-new', username: '新成员', github_login: 'newbie', avatar_url: '', global_role: 'user' },
  ]);
  api.setAdminRole.mockResolvedValue(undefined);
  render(<AdminsPage />);

  fireEvent.click(await screen.findByRole('button', { name: '添加平台管理员' }));
  const dialog = await screen.findByRole('dialog', { name: '添加平台管理员' });
  fireEvent.change(within(dialog).getByLabelText('搜索用户'), { target: { value: 'new' } });

  // AutoComplete 下拉经 portal 渲染到 body，不在 Modal dialog 内，用全局查询
  const option = await screen.findByText('新成员');
  fireEvent.click(option);

  await waitFor(() => expect(api.searchAdminCandidates).toHaveBeenCalledWith('new'));
  await waitFor(() => expect(api.setAdminRole).toHaveBeenCalledWith('u-new'));
  expect(screen.queryByRole('dialog', { name: '添加平台管理员' })).not.toBeInTheDocument();
});

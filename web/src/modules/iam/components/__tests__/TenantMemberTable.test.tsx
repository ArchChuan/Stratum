import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { TenantMemberTable } from '../TenantMemberTable';

let mobile = true;

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => ({ isMobile: mobile }),
}));

const member = {
  user_id: 'user-2',
  github_login: 'octocat',
  avatar_url: '',
  role: 'member',
  joined_at: '2026-07-10T00:00:00Z',
};

beforeAll(() => {
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    addListener: vi.fn(),
    removeListener: vi.fn(),
  })));
});

describe('TenantMemberTable', () => {
  beforeEach(() => {
    mobile = true;
  });

  it('shows member details and authorized actions on mobile', async () => {
    const onRemove = vi.fn();
    const onChange = vi.fn();
    render(
      <TenantMemberTable
        members={[member]}
        loading={false}
        currentUserSub="owner-1"
        currentUserRole="owner"
        isOwner
        pagination={{ current: 1, pageSize: 1, total: 2 }}
        onRemove={onRemove}
        onRoleChange={vi.fn()}
        onChange={onChange}
      />,
    );

    expect(screen.getByText('octocat')).toBeInTheDocument();
    expect(screen.getByText('普通成员')).toBeInTheDocument();
    expect(screen.getByText(new Date(member.joined_at).toLocaleDateString('zh-CN'))).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '变更角色' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '移除' }));
    const confirmation = await screen.findByText('确认移除该成员？');
    const popover = confirmation.closest('.ant-popover');
    fireEvent.click(within(popover as HTMLElement).getByRole('button', { name: /移\s*除/ }));
    await waitFor(() => expect(onRemove).toHaveBeenCalledWith('user-2'));
    fireEvent.click(document.querySelector<HTMLButtonElement>('.ant-pagination-next button')!);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ current: 2, pageSize: 1, total: 2 }),
      {},
      {},
      { action: 'paginate', currentDataSource: [member] },
    );
    expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
  });

  it('keeps the desktop table', () => {
    mobile = false;
    render(
      <TenantMemberTable
        members={[member]}
        loading={false}
        isOwner={false}
        pagination={{ current: 1, pageSize: 20, total: 1 }}
        onRemove={vi.fn()}
        onRoleChange={vi.fn()}
        onChange={vi.fn()}
      />,
    );

    expect(document.querySelector('.ant-table')).toBeInTheDocument();
  });
});

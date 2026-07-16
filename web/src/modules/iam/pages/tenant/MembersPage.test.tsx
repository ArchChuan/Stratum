import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useTenantMembers } from '../../hooks/useTenantMembers';

import { MembersPage } from './MembersPage';

vi.mock('../../hooks/useTenantMembers', () => ({
  useTenantMembers: vi.fn(),
}));

vi.mock('../../components/TenantInviteModal', () => ({
  TenantInviteModal: () => null,
}));

describe('MembersPage pagination', () => {
  const fetchPage = vi.fn();

  beforeEach(() => {
    const getComputedStyle = window.getComputedStyle.bind(window);
    vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
    fetchPage.mockReset();
    vi.mocked(useTenantMembers).mockReturnValue({
      user: { sub: 'owner-1', role: 'owner', avatar_url: '', github_login: 'owner' },
      members: Array.from({ length: 20 }, (_, index) => ({
        user_id: `user-${index + 1}`,
        github_login: `member-${index + 1}`,
        avatar_url: '',
        role: index === 0 ? 'owner' : 'member',
        joined_at: '2026-07-14T00:00:00Z',
      })),
      loading: false,
      pagination: { current: 1, pageSize: 20, total: 25 },
      fetchPage,
      inviteOpen: false,
      setInviteOpen: vi.fn(),
      inviteLoading: false,
      isOwner: true,
      canInvite: true,
      handleInvite: vi.fn(),
      handleRemove: vi.fn(),
      handleRoleChange: vi.fn(),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('requests the next server-side page from the table control', () => {
    const { container } = render(<MembersPage />);

    expect(screen.getByText('共 25 位成员')).toBeInTheDocument();
    const nextButton = container.querySelector<HTMLButtonElement>('.ant-pagination-next button');
    expect(nextButton).not.toBeNull();
    fireEvent.click(nextButton!);

    expect(fetchPage).toHaveBeenCalledWith(2, 20);
  });
});

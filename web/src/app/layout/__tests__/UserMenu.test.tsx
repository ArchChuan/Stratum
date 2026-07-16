import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { UserMenu } from '../UserMenu';

import { memoryUserApi } from '@/modules/memory/api/memory-user.api';

const authState = vi.hoisted(() => ({ role: 'member' }));

beforeAll(() => {
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
});

vi.mock('@/modules/iam', () => ({
  useAuth: () => ({
    user: {
      github_login: 'tester',
      role: authState.role,
      current_tenant: { id: 'tenant-1', name: 'Test', role: authState.role },
    },
    logout: vi.fn(),
  }),
}));

vi.mock('@/modules/memory/api/memory-user.api', () => ({
  memoryUserApi: { clearMyMemories: vi.fn() },
}));

vi.mock('antd', async (importOriginal) => {
  const actual = await importOriginal<typeof import('antd')>();
  return {
    ...actual,
    message: { success: vi.fn(), error: vi.fn() },
  };
});

const renderMenu = () => render(
  <MemoryRouter future={{ v7_relativeSplatPath: true, v7_startTransition: true }}>
    <UserMenu />
  </MemoryRouter>,
);

const openClearConfirmation = async () => {
  fireEvent.click(screen.getByRole('button', { name: '打开用户菜单' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: '清空我的记忆' }));
  return screen.findByRole('dialog', { name: '清空我的记忆' });
};

describe('UserMenu memory interaction', () => {
  beforeEach(() => {
    authState.role = 'member';
    vi.mocked(memoryUserApi.clearMyMemories).mockReset();
    vi.mocked(message.success).mockReset();
    vi.mocked(message.error).mockReset();
  });

  it.each(['member', 'admin'])('offers personal memory clearing to %s users', async (role) => {
    authState.role = role;
    renderMenu();

    fireEvent.click(screen.getByRole('button', { name: '打开用户菜单' }));

    expect(await screen.findByRole('menuitem', { name: '清空我的记忆' })).toBeInTheDocument();
  });

  it('clears once after confirmation and reports success', async () => {
    vi.mocked(memoryUserApi.clearMyMemories).mockResolvedValue();
    renderMenu();
    await openClearConfirmation();

    fireEvent.click(screen.getByRole('button', { name: '确认清空' }));

    await waitFor(() => expect(memoryUserApi.clearMyMemories).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(message.success).toHaveBeenCalledWith('记忆已清空', 2));
  });

  it('shows the backend failure without auto-dismiss', async () => {
    vi.mocked(memoryUserApi.clearMyMemories).mockRejectedValue(new Error('记忆服务暂不可用'));
    renderMenu();
    await openClearConfirmation();

    fireEvent.click(screen.getByRole('button', { name: '确认清空' }));

    await waitFor(() => expect(message.error).toHaveBeenCalledWith('记忆服务暂不可用', 0));
  });
});

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { DashboardPage } from '../DashboardPage';

const useDashboardPageMock = vi.hoisted(() => vi.fn());

vi.mock('../../hooks/useDashboardPage', () => ({
  useDashboardPage: useDashboardPageMock,
}));

vi.mock('../../components/RecentExecutionsTable', () => ({
  RecentExecutionsTable: () => <div>最近执行记录</div>,
}));

describe('DashboardPage', () => {
  beforeEach(() => {
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
  });

  it('does not render execution history content', () => {
    useDashboardPageMock.mockReturnValue({
      counts: { skills: 2, agents: 3, mcpServers: 1, knowledge: 4 },
      loading: false,
    });

    render(<DashboardPage />);

    expect(screen.queryByText('近期执行')).not.toBeInTheDocument();
    expect(screen.queryByText('最近执行记录')).not.toBeInTheDocument();
    expect(screen.queryByText(/执行历史/)).not.toBeInTheDocument();
    expect(screen.getByText('概览')).toBeInTheDocument();
    expect(screen.getByText('系统运行状态一览')).toBeInTheDocument();
  });
});

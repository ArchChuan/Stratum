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

  it('renders eight responsive tenant overview cards and the recent executions section', () => {
    useDashboardPageMock.mockReturnValue({
      counts: { agents: 1, skills: 2, knowledge_workspaces: 3, mcp_servers: 4,
        model_providers: 5, tenant_members: 6, workflows: 7, agent_user_messages_7d: 8 },
      loading: false,
      executions: [],
      executionsTotal: 0,
      executionsLoading: false,
      page: 1,
      pageSize: 10,
      handlePageChange: vi.fn(),
    });

    render(<DashboardPage />);

    expect(screen.getByText('概览')).toBeInTheDocument();
    expect(screen.getByText('系统运行状态一览')).toBeInTheDocument();
    expect(screen.getByText('最近执行')).toBeInTheDocument();
    expect(screen.getByText('最近执行记录')).toBeInTheDocument();
    for (const label of ['模型厂商', '租户成员', '工作流', '近七日 Agent 对话']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    const cards = document.querySelectorAll('.ant-col');
    expect(cards).toHaveLength(8);
    cards.forEach((card) => {
      expect(card).toHaveClass('ant-col-xs-24', 'ant-col-sm-12', 'ant-col-lg-6');
    });
  });
});

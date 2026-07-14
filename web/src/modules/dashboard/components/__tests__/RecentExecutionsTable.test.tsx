import { render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { RecentExecutionsTable } from '../RecentExecutionsTable';

let mobile = true;

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => ({ isMobile: mobile }),
}));

const execution = {
  id: 'exec-1',
  agent_name: '客服 Agent',
  status: 'success',
  input_preview: '查询订单进度',
  output_preview: '订单正在配送',
  total_tokens: 1234,
  duration_ms: 2500,
  created_at: '2026-07-14T02:03:00Z',
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

describe('RecentExecutionsTable', () => {
  beforeEach(() => {
    mobile = true;
  });

  it('shows execution identity, status, key metrics and time in a mobile card', () => {
    render(<RecentExecutionsTable data={[execution]} loading={false} />);

    expect(screen.getByText('客服 Agent')).toBeInTheDocument();
    expect(screen.getByText('成功')).toBeInTheDocument();
    expect(screen.getByText('查询订单进度')).toBeInTheDocument();
    expect(screen.getByText('1,234 Token')).toBeInTheDocument();
    expect(screen.getByText('2.5s')).toBeInTheDocument();
    expect(screen.getByText(new Date(execution.created_at).toLocaleString('zh-CN'))).toBeInTheDocument();
    expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
  });

  it('keeps the desktop table', () => {
    mobile = false;
    render(<RecentExecutionsTable data={[execution]} loading={false} />);

    expect(document.querySelector('.ant-table')).toBeInTheDocument();
  });
});

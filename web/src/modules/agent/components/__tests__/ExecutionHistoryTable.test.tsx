import { render, screen } from '@testing-library/react';
import { beforeAll, expect, it, vi } from 'vitest';

import { ExecutionHistoryTable } from '../ExecutionHistoryTable';

vi.mock('@/shared/hooks', () => ({ useResponsive: () => ({ isMobile: true }) }));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

it('renders execution identity, failure, metrics and time as a mobile card', () => {
  const createdAt = '2026-07-14T02:03:00Z';
  render(
    <ExecutionHistoryTable
      executions={[{
        id: 'exec-1',
        agent_name: '诊断 Agent',
        status: 'error',
        input_preview: '分析故障',
        error_message: '工具调用超时',
        total_tokens: 765,
        duration_ms: 1250,
        created_at: createdAt,
      }]}
      loading={false}
      pagination={{ current: 1, pageSize: 10, total: 1 }}
      onChange={vi.fn()}
    />,
  );

  expect(screen.getByText('诊断 Agent')).toBeInTheDocument();
  expect(screen.getByText('失败')).toBeInTheDocument();
  expect(screen.getByText('工具调用超时')).toBeInTheDocument();
  expect(screen.getByText('765 Token')).toBeInTheDocument();
  expect(screen.getByText('1.3s')).toBeInTheDocument();
  expect(screen.getByText(new Date(createdAt).toLocaleString('zh-CN'))).toBeInTheDocument();
  expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
});

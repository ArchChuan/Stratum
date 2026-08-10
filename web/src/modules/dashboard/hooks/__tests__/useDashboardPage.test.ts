import { renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useDashboardPage } from '../useDashboardPage';

const overview = vi.hoisted(() => vi.fn());
const executions = vi.hoisted(() => vi.fn());
vi.mock('../../api/dashboard.api', () => ({ dashboardApi: { overview, executions } }));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);

const counts = { agents: 1, skills: 2, knowledge_workspaces: 3, mcp_servers: 4,
  model_providers: 5, tenant_members: 6, workflows: 7, agent_user_messages_7d: 8 };

const execution = {
  id: 'exec-1',
  trace_id: 'trace-1',
  agent_id: 'agent-1',
  agent_name: '客服 Agent',
  status: 'success',
  input_preview: '查询订单进度',
  output_preview: '订单正在配送',
  error_message: '',
  total_tokens: 1234,
  duration_ms: 2500,
  created_at: '2026-07-14T02:03:00Z',
};

describe('useDashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    overview.mockResolvedValue(counts);
    executions.mockResolvedValue({ executions: [execution], total: 1 });
  });

  it('loads overview counts and first execution page in parallel', async () => {
    const { result } = renderHook(() => useDashboardPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(overview).toHaveBeenCalledTimes(1);
    expect(executions).toHaveBeenCalledWith({ page: 1, pageSize: 10 });
    expect(result.current.counts).toEqual(counts);
    expect(result.current.executions).toEqual([execution]);
    expect(result.current.executionsTotal).toBe(1);
    expect(result.current.page).toBe(1);
  });

  it('keeps zero defaults and reports a persistent error when overview fails', async () => {
    overview.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useDashboardPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(Object.values(result.current.counts).every((value) => value === 0)).toBe(true);
    expect(message.error).toHaveBeenCalledWith({ content: '加载概览数据失败', duration: 0 });
    // 执行表一侧成功不应被概览失败拖累。
    expect(result.current.executions).toEqual([execution]);
  });

  it('keeps counts and reports an error when the execution list fails', async () => {
    executions.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useDashboardPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.counts).toEqual(counts);
    expect(message.error).toHaveBeenCalledWith({ content: '加载执行记录失败', duration: 0 });
  });

  it('refetches the execution page on page change without reloading counts', async () => {
    executions.mockResolvedValue({ executions: [], total: 3 });
    const { result } = renderHook(() => useDashboardPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    executions.mockResolvedValue({ executions: [execution], total: 3 });

    await result.current.handlePageChange(2, 10);
    await waitFor(() => expect(result.current.page).toBe(2));
    expect(executions).toHaveBeenLastCalledWith({ page: 2, pageSize: 10 });
    expect(result.current.executions).toEqual([execution]);
    expect(result.current.executionsTotal).toBe(3);
    expect(overview).toHaveBeenCalledTimes(1);
  });
});

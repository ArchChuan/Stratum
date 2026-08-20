import { renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useDashboardPage } from '../useDashboardPage';

const overview = vi.hoisted(() => vi.fn());
vi.mock('../../api/dashboard.api', () => ({ dashboardApi: { overview } }));
vi.spyOn(message, 'error').mockImplementation(() => undefined as never);

const counts = { agents: 1, skills: 2, knowledge_workspaces: 3, mcp_servers: 4,
  model_providers: 5, tenant_members: 6, workflows: 7, agent_user_messages_7d: 8 };

describe('useDashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    overview.mockResolvedValue(counts);
  });

  it('loads overview counts', async () => {
    const { result } = renderHook(() => useDashboardPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(overview).toHaveBeenCalledTimes(1);
    expect(result.current.counts).toEqual(counts);
  });

  it('keeps zero defaults and reports a persistent error when overview fails', async () => {
    overview.mockRejectedValue(new Error('failed'));
    const { result } = renderHook(() => useDashboardPage());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(Object.values(result.current.counts).every((value) => value === 0)).toBe(true);
    expect(message.error).toHaveBeenCalledWith({ content: '加载概览数据失败', duration: 3 });
  });
});

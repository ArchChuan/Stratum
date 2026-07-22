import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useDashboardPage } from '../useDashboardPage';

const apiMocks = vi.hoisted(() => ({
  skillList: vi.fn(),
  agentList: vi.fn(),
  executions: vi.fn(),
  mcpList: vi.fn(),
  knowledgeList: vi.fn(),
}));

vi.mock('@/modules/skill', () => ({ skillApi: { list: apiMocks.skillList } }));
vi.mock('@/modules/agent', () => ({ agentApi: {
  list: apiMocks.agentList,
  executions: apiMocks.executions,
} }));
vi.mock('@/modules/mcp', () => ({ mcpApi: { list: apiMocks.mcpList } }));
vi.mock('@/modules/knowledge', () => ({ knowledgeApi: { list: apiMocks.knowledgeList } }));

describe('useDashboardPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.skillList.mockResolvedValue([]);
    apiMocks.agentList.mockResolvedValue([]);
    apiMocks.mcpList.mockResolvedValue([]);
    apiMocks.knowledgeList.mockResolvedValue([]);
  });

  it('does not request unavailable execution history data', async () => {
    const { result } = renderHook(() => useDashboardPage());

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(apiMocks.executions).not.toHaveBeenCalled();
    expect(result.current.counts).toEqual({
      skills: 0,
      agents: 0,
      mcpServers: 0,
      knowledge: 0,
    });
  });
});

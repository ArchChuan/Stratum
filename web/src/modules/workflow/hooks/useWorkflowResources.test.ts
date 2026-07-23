import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useWorkflowResources } from './useWorkflowResources';

const mocks = vi.hoisted(() => ({ listAgents: vi.fn(), listSkills: vi.fn(), listMCPServers: vi.fn() }));
vi.mock('@/modules/agent/api/agent.api', () => ({ agentApi: { list: mocks.listAgents } }));
vi.mock('@/modules/skill/api/skill.api', () => ({ skillApi: { list: mocks.listSkills } }));
vi.mock('@/modules/mcp/api/mcp.api', () => ({ mcpApi: { list: mocks.listMCPServers } }));
vi.mock('antd', async (importOriginal) => {
  const actual = await importOriginal<typeof import('antd')>();
  return { ...actual, message: { error: vi.fn() } };
});

describe('useWorkflowResources', () => {
  beforeEach(() => vi.clearAllMocks());

  it('loads real workflow executor resources and only offers published skill revisions', async () => {
    mocks.listAgents.mockResolvedValue([{ id: 'agent-1', name: '研究 Agent' }]);
    mocks.listSkills.mockResolvedValue([
      { id: 'skill-1', name: '检索', activeRevisionId: 'revision-2' },
      { id: 'skill-2', name: '草稿 Skill' },
    ]);
    mocks.listMCPServers.mockResolvedValue([{ id: 'mcp-1', name: '知识服务' }]);

    const { result } = renderHook(() => useWorkflowResources());
    await waitFor(() => expect(result.current.agents).toHaveLength(1));

    expect(result.current.skills).toEqual([{ value: 'skill-1', label: '检索' }]);
    expect(result.current.skillRevisions).toEqual([{ value: 'revision-2', label: '检索（已发布）' }]);
    expect(result.current.mcpServers).toEqual([{ value: 'mcp-1', label: '知识服务' }]);
  });

  it('exposes partial resource loading failures without discarding successful options', async () => {
    mocks.listAgents.mockRejectedValue({ response: { data: { error: 'Agent 加载失败' } } });
    mocks.listSkills.mockResolvedValue([]);
    mocks.listMCPServers.mockResolvedValue([{ id: 'mcp-1', name: '知识服务' }]);

    const { result } = renderHook(() => useWorkflowResources());
    await waitFor(() => expect(message.error).toHaveBeenCalledWith({ content: 'Agent 加载失败', duration: 0 }));
    await act(async () => {});

    expect(result.current.mcpServers).toEqual([{ value: 'mcp-1', label: '知识服务' }]);
  });
});

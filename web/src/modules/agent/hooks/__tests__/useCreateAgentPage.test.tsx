import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { agentApi } from '../../api/agent.api';
import { useCreateAgentPage } from '../useCreateAgentPage';

const mocks = vi.hoisted(() => ({ navigate: vi.fn() }));

vi.mock('react-router-dom', async (importOriginal) => ({
  ...await importOriginal<typeof import('react-router-dom')>(),
  useNavigate: () => mocks.navigate,
}));
vi.mock('../../api/agent.api', () => ({ agentApi: { create: vi.fn() } }));
vi.mock('@/modules/skill', () => ({ skillApi: { list: vi.fn().mockResolvedValue([]) } }));
vi.mock('@/modules/mcp', () => ({ mcpApi: { toolOptions: vi.fn().mockResolvedValue([]) } }));
vi.mock('@/modules/knowledge', () => ({ knowledgeApi: { list: vi.fn().mockResolvedValue([]) } }));
vi.mock('@/modules/llm', () => ({ llmApi: { listModels: vi.fn().mockResolvedValue([]), listProviders: vi.fn().mockResolvedValue([]) } }));

describe('useCreateAgentPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('defaults memoryScope to user on create when the form field is absent', async () => {
    const { result } = renderHook(() => useCreateAgentPage());
    await waitFor(() => expect(result.current.form).toBeDefined());

    await act(async () => {
      await result.current.onFinish({
        name: 'new-agent',
        description: '',
        systemPrompt: '',
        llmModel: 'qwen-plus',
        maxIterations: 10,
        maxContextTokens: 0,
      });
    });

    expect(agentApi.create).toHaveBeenCalledWith(expect.objectContaining({ memoryScope: 'user' }));
    // 显式传值不被覆盖。
    await act(async () => {
      await result.current.onFinish({
        name: 'agent-2',
        llmModel: 'qwen-plus',
        maxIterations: 10,
        maxContextTokens: 0,
        memoryScope: 'agent',
      });
    });
    expect(agentApi.create).toHaveBeenLastCalledWith(expect.objectContaining({ memoryScope: 'agent' }));
  });
});

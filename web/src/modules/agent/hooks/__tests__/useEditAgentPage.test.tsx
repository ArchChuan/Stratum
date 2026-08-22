import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { agentApi } from '../../api/agent.api';
import type { Agent } from '../../model/agent';
import { useEditAgentPage } from '../useEditAgentPage';

const mocks = vi.hoisted(() => ({ id: 'system', navigate: vi.fn() }));

vi.mock('react-router-dom', async (importOriginal) => ({
  ...await importOriginal<typeof import('react-router-dom')>(),
  useParams: () => ({ id: mocks.id }),
  useNavigate: () => mocks.navigate,
}));
vi.mock('../../api/agent.api', () => ({ agentApi: { get: vi.fn(), update: vi.fn() } }));
vi.mock('@/modules/skill', () => ({ skillApi: { list: vi.fn() } }));
vi.mock('@/modules/mcp', () => ({ mcpApi: { toolOptions: vi.fn() } }));
vi.mock('@/modules/knowledge', () => ({ knowledgeApi: { list: vi.fn() } }));
vi.mock('@/modules/llm', () => ({ llmApi: { getCatalogue: vi.fn().mockResolvedValue({ chatModels: [], embeddingModels: [] }) } }));

const agent = (id: string, isSystem: boolean): Agent => ({
  id,
  name: id,
  description: '',
  type: 'react',
  systemPrompt: '',
  llmModel: 'glm-5.2',
  allowedSkills: [],
  mcpToolIds: [],
  knowledgeWorkspaceIds: [],
  memoryScope: 'user',
  isSystem,
});

describe('useEditAgentPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.id = 'system';
  });

  it('clears the previous agent while a changed route id is loading', async () => {
    let resolveOrdinary!: (value: Agent) => void;
    const ordinary = new Promise<Agent>((resolve) => { resolveOrdinary = resolve; });
    vi.mocked(agentApi.get)
      .mockResolvedValueOnce(agent('system', true))
      .mockReturnValueOnce(ordinary);

    const { result, rerender } = renderHook(() => useEditAgentPage());
    await waitFor(() => expect(result.current.agent?.isSystem).toBe(true));

    mocks.id = 'ordinary';
    rerender();

    expect(result.current.pageLoading).toBe(true);
    expect(result.current.agent).toBeUndefined();

    await act(async () => resolveOrdinary(agent('ordinary', false)));
  });

  it.each([
		{ isSystem: true, wantPath: '/agents' },
		{ isSystem: false, wantPath: '/agents' },
	])('returns to the correct management tab after saving', async ({ isSystem, wantPath }) => {
		vi.mocked(agentApi.get).mockResolvedValue(agent(mocks.id, isSystem));
		vi.mocked(agentApi.update).mockResolvedValue({} as never);
		const { result } = renderHook(() => useEditAgentPage());
		await waitFor(() => expect(result.current.agent?.isSystem).toBe(isSystem));

		await act(async () => result.current.onFinish({
			name: mocks.id,
			description: '',
			systemPrompt: '',
			llmModel: 'glm-5.2',
			maxIterations: 10,
			maxContextTokens: 8000,
			allowedSkills: [],
			mcpToolIds: [],
			knowledgeWorkspaceIds: [],
			memoryScope: 'user',
		}));

		expect(mocks.navigate).toHaveBeenCalledWith(wantPath);
	});

});

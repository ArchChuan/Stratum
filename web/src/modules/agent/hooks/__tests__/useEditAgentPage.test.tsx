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
vi.mock('@/modules/llm', () => ({
  llmApi: {
    // 编辑页初始加载拉取模型目录与厂商；listModels/listProviders 缺失会让
    // Promise.allSettled 前同步抛错，误走「加载失败 → 跳回列表页」分支。
    listModels: vi.fn().mockResolvedValue([]),
    listProviders: vi.fn().mockResolvedValue([]),
  },
}));

// F2：hook 内 useAuth/useTenantRole/useEditorCandidates 由 iam 提供，测试直接 mock 返回值。
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'system' } }),
  useTenantRole: () => ({ role: 'member', isAdmin: false, isOwner: false, isMember: true, hasTenantRole: () => false }),
  useEditorCandidates: () => ({ candidates: [], loading: false }),
}));

const agent = (id: string): Agent => ({
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
      .mockResolvedValueOnce(agent('system'))
      .mockReturnValueOnce(ordinary);

    const { result, rerender } = renderHook(() => useEditAgentPage());
    await waitFor(() => expect(result.current.agent?.id).toBe('system'));

    mocks.id = 'ordinary';
    rerender();

    expect(result.current.pageLoading).toBe(true);
    expect(result.current.agent).toBeUndefined();

    await act(async () => resolveOrdinary(agent('ordinary')));
  });

  it('保存后原地刷新：不离开编辑页，重拉 agent 回填表单并 bump refreshTick', async () => {
		vi.mocked(agentApi.get).mockResolvedValue(agent(mocks.id));
		vi.mocked(agentApi.update).mockResolvedValue({} as never);
		const { result } = renderHook(() => useEditAgentPage());
		await waitFor(() => expect(result.current.agent?.id).toBe(mocks.id));

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

		// 产品行为：保存不再跳回列表页，而是原地刷新（重拉 agent + 版本历史 tab 更新）。
		expect(mocks.navigate).not.toHaveBeenCalled();
		await waitFor(() => expect(vi.mocked(agentApi.get)).toHaveBeenCalledTimes(2));
		expect(result.current.refreshTick).toBe(1);
	});

});

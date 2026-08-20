import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { StreamSnapshot } from '../ChatStreamContext';
import { useChatPage } from '../useChatPage';

const mocks = vi.hoisted(() => ({
  approval: null as null | {
    approvalId: string;
    agentId: string;
    toolName: string;
    serverId: string;
    riskLevel: string;
    status: string;
  },
  listAgents: vi.fn(),
	getAgent: vi.fn(),
  listConversations: vi.fn(),
	createConversation: vi.fn(),
  decide: vi.fn(),
  listApprovals: vi.fn(),
  resume: vi.fn(),
  stream: {
    streamConversationId: null as string | null,
    accumulatedContent: '',
    streamResult: null as null | {
      output?: string;
      sources?: unknown[];
      noAnswer?: { Reason?: string; RetrievedCount?: number };
      artifacts?: Array<{ type: string; diagnosticReport?: { facts?: unknown[] } }>;
    },
    streamError: null,
		streamFailure: null as null | { message: string; code?: string; status?: number },
    streamDone: false,
    streamApproval: null as null | Record<string, string>,
    startStream: vi.fn(),
    cancelStream: vi.fn(),
		clearStreamFailure: vi.fn(),
    getStreamState: vi.fn<() => StreamSnapshot>(() => ({
      streaming: false,
      conversationId: null,
      userQuery: null,
      content: '',
      done: false,
      result: null,
      error: null,
      approval: null,
      executionId: null,
    })),
  },
}));

vi.mock('../../api/agent.api', () => ({
  agentApi: {
    list: mocks.listAgents,
		get: mocks.getAgent,
    listToolApprovals: mocks.listApprovals,
    decideToolApproval: mocks.decide,
    resumeToolApproval: mocks.resume,
  },
  conversationApi: {
    list: mocks.listConversations,
		create: mocks.createConversation,
    messages: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock('../ChatStreamContext', () => ({
  useChatStream: () => mocks.stream,
}));

vi.mock('antd', () => ({
  message: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

describe('useChatPage tool approvals', () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.clearAllMocks();
    mocks.listAgents.mockResolvedValue([]);
		mocks.getAgent.mockResolvedValue({ id: 'system', name: '平台助手', isSystem: true });
    mocks.listConversations.mockResolvedValue([]);
		mocks.createConversation.mockResolvedValue({ id: 'conversation-new', name: '新会话' });
    mocks.listApprovals.mockResolvedValue(mocks.approval ? [mocks.approval] : []);
    mocks.decide.mockResolvedValue({});
    mocks.stream.streamConversationId = null;
    mocks.stream.accumulatedContent = '';
    mocks.stream.streamDone = false;
    mocks.stream.streamApproval = null;
		mocks.stream.streamFailure = null;
  });

  it('turns an unknown resume result into non-retryable reconciliation work', async () => {
    mocks.approval = {
      approvalId: 'approval-1',
      agentId: 'agent-1',
      toolName: 'delete',
      serverId: 'orders',
      riskLevel: 'destructive',
      status: 'pending',
    };
    mocks.listApprovals.mockResolvedValue([mocks.approval]);
    mocks.resume.mockRejectedValue({
      response: { data: { error: 'tool approval outcome is unknown' } },
    });

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.pendingApprovals).toHaveLength(1));

    await act(async () => result.current.handleApprove('approval-1'));

    expect(result.current.pendingApprovals[0]?.status).toBe('unknown_outcome');
    expect(message.error).toHaveBeenCalledWith({
      content: '工具执行结果未知，需要人工对账',
      duration: 3,
    });
  });

  it('replaces an empty streamed assistant message with an approval waiting state', async () => {
    mocks.approval = null;
    mocks.listAgents.mockResolvedValue([{ id: 'agent-1', name: 'Agent' }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);

    const { result, rerender } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.agents).toHaveLength(1));
    act(() => result.current.setSelectedAgent('agent-1'));
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    act(() => result.current.setInput('执行删除'));
    act(() => result.current.handleSend());
    expect(result.current.messages[result.current.messages.length - 1]?.content).toBe('');

    mocks.stream.streamConversationId = 'conversation-1';
    mocks.stream.streamDone = true;
    mocks.stream.streamApproval = {
      approvalId: 'approval-2',
      agentId: 'agent-1',
      toolName: 'delete',
      serverId: 'orders',
      riskLevel: 'destructive',
      status: 'pending',
    };
    rerender();

    await waitFor(() => {
      expect(result.current.messages.some((item) => item.content === '工具调用等待审批')).toBe(true);
    });
  });

  it('orders a stale API response system-first and selects it by default', async () => {
    mocks.listAgents.mockResolvedValue([
      { id: 'regular', name: '普通 Agent', isSystem: false },
      { id: 'system', name: '平台使用小助手', isSystem: true },
    ]);

    const { result } = renderHook(() => useChatPage());

    await waitFor(() => expect(result.current.agents.map((agent) => agent.id)).toEqual([
      'system', 'regular',
    ]));
    expect(result.current.selectedAgent).toBe('system');
  });

  it('agents 存在但无 isSystem 时回退选择第一个 agent，会话列表照常加载', async () => {
    mocks.listAgents.mockResolvedValue([
      { id: 'regular-1', name: '普通 Agent A', isSystem: false },
      { id: 'regular-2', name: '普通 Agent B', isSystem: false },
    ]);
    mocks.listConversations.mockResolvedValue([{ id: 'conv-1', name: '会话' }]);

    const { result } = renderHook(() => useChatPage());

    // 不再回退到 null（null 会让 conversations effect 直接 return，会话列表永不加载）
    await waitFor(() => expect(result.current.selectedAgent).toBe('regular-1'));
    await waitFor(() => expect(result.current.selectedConv).toBe('conv-1'));
    expect(result.current.conversations).toHaveLength(1);
  });

  it('agents 为空且无 isSystem 时 selectedAgent 为 null 但加载态结束', async () => {
    mocks.listAgents.mockResolvedValue([]);

    const { result } = renderHook(() => useChatPage());

    await waitFor(() => expect(result.current.agentsLoading).toBe(false));
    expect(result.current.agents).toHaveLength(0);
    expect(result.current.selectedAgent).toBeNull();
  });

  it('hydrates streamed structured artifacts into the completed assistant message', async () => {
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    const { result, rerender } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    act(() => result.current.setInput('诊断'));
    act(() => result.current.handleSend());

    mocks.stream.streamConversationId = 'conversation-1';
    mocks.stream.streamDone = true;
    mocks.stream.streamResult = {
      output: '诊断完成',
      artifacts: [{ type: 'diagnostic_report', diagnosticReport: { facts: [] } }],
    };
    rerender();

    await waitFor(() => expect(
      result.current.messages[result.current.messages.length - 1]?.artifacts,
    ).toHaveLength(1));
    expect(
      result.current.messages[result.current.messages.length - 1]
        ?.artifacts?.[0]?.diagnosticReport?.evidenceGaps,
    ).toEqual([]);
  });

	it('loads and creates conversations only for a fixed platform assistant', async () => {
		const fixedID = 'stratum-platform-assistant';
		mocks.getAgent.mockResolvedValue({ id: fixedID, name: 'Stratum 平台助手', isSystem: true });
		const { result } = renderHook(() => useChatPage({ fixedAgentId: fixedID }));

		await waitFor(() => expect(result.current.selectedAgent).toBe(fixedID));
		expect(mocks.listAgents).not.toHaveBeenCalled();
		expect(mocks.getAgent).toHaveBeenCalledWith(fixedID);
		await act(async () => result.current.handleCreateConv());
		expect(mocks.createConversation).toHaveBeenCalledWith(fixedID);
	});

	it('copies noAnswer from the SSE done payload into the assistant message', async () => {
		mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
		mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
		const { result, rerender } = renderHook(() => useChatPage());
		await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
		act(() => result.current.setInput('哪些文档没有？'));
		act(() => result.current.handleSend());

		mocks.stream.streamConversationId = 'conversation-1';
		mocks.stream.streamDone = true;
		mocks.stream.streamResult = {
			output: '未基于知识库作答',
			sources: [],
			noAnswer: { Reason: 'no_sources', RetrievedCount: 0 },
		};
		rerender();

		await waitFor(() => {
			const last = result.current.messages[result.current.messages.length - 1];
			expect(last?.noAnswer?.Reason).toBe('no_sources');
			expect(last?.sources).toEqual([]);
		});
	});

	it('copies noAnswer from the restored stream result during conversation restore', async () => {
		mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
		mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
		mocks.stream.getStreamState.mockReturnValue({
			streaming: false,
			conversationId: 'conversation-1',
			userQuery: '重试的问题',
			content: '未基于知识库作答',
			done: true,
			result: {
				output: '未基于知识库作答',
				sources: [],
				noAnswer: { Reason: 'threshold_filtered', RetrievedCount: 5 },
			},
			error: null,
			approval: null,
			executionId: null,
		});

		const { result } = renderHook(() => useChatPage());
		await waitFor(() => expect(result.current.selectedAgent).toBe('system'));
		act(() => result.current.setSelectedAgent('system'));
		await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));

		await waitFor(() => {
			expect(result.current.messages.some((item) => item.noAnswer?.Reason === 'threshold_filtered')).toBe(true);
		});
	});
});

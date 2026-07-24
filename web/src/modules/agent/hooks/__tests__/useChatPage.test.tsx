import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

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
  listConversations: vi.fn(),
  decide: vi.fn(),
  listApprovals: vi.fn(),
  resume: vi.fn(),
  stream: {
    streamConversationId: null as string | null,
    accumulatedContent: '',
    streamResult: null as null | {
      output?: string;
      artifacts?: Array<{ type: string; diagnosticReport?: { facts?: unknown[] } }>;
    },
    streamError: null,
    streamDone: false,
    streamApproval: null as null | Record<string, string>,
    startStream: vi.fn(),
    cancelStream: vi.fn(),
    getStreamState: vi.fn(() => ({
      streaming: false,
      conversationId: null,
      userQuery: null,
      content: '',
      done: false,
      result: null,
      error: null,
      approval: null,
    })),
  },
}));

vi.mock('../../api/agent.api', () => ({
  agentApi: {
    list: mocks.listAgents,
    listToolApprovals: mocks.listApprovals,
    decideToolApproval: mocks.decide,
    resumeToolApproval: mocks.resume,
  },
  conversationApi: {
    list: mocks.listConversations,
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
    mocks.listConversations.mockResolvedValue([]);
    mocks.listApprovals.mockResolvedValue(mocks.approval ? [mocks.approval] : []);
    mocks.decide.mockResolvedValue({});
    mocks.stream.streamConversationId = null;
    mocks.stream.accumulatedContent = '';
    mocks.stream.streamDone = false;
    mocks.stream.streamApproval = null;
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
      duration: 0,
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
});

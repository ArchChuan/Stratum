import { act, renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import type { MutableRefObject } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { conversationApi } from '../../api/agent.api';
import type { ChatMessage } from '../../model/agent';
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
    conversationId?: string;
    userId?: string;
  },
  listAgents: vi.fn(),
	getAgent: vi.fn(),
  listConversations: vi.fn(),
	createConversation: vi.fn(),
  decide: vi.fn(),
  listApprovals: vi.fn(),
  resume: vi.fn(),
  cancelToolApproval: vi.fn(),
  getActiveExecution: vi.fn(),
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
    streamApprovals: [] as Record<string, string>[],
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
      approvals: [],
      delegateStatus: null,
      executionId: null,
      conflict: false,
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
    cancelToolApproval: mocks.cancelToolApproval,
    getActiveExecution: mocks.getActiveExecution,
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
    warning: vi.fn(),
  },
}));

// useChatPage 现 import useAuth(iam barrel);mock 隔离,避免真加载 iam 页面模块
// (ProfilePage 等消费 antd Typography)导致 antd mock 缺导出。
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'test-user', tenant_id: 't1', role: 'member' } }),
}));

describe('useChatPage tool approvals', () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.clearAllMocks();
    mocks.listAgents.mockResolvedValue([]);
		mocks.getAgent.mockResolvedValue({ id: 'system', name: '平台助手' });
    mocks.listConversations.mockResolvedValue([]);
		mocks.createConversation.mockResolvedValue({ id: 'conversation-new', name: '新会话' });
    // 动态读取当前 mocks.approval：mockResolvedValue 在 beforeEach 求值会把测试体内
    // 的设置锁死为 null → listToolApprovals 恒返回 []，恢复路径 fallback 补终态卡
    // （status=active.approvalStatus）→ waitingApprovals 不命中，轮询终态续跑不触发。
    mocks.listApprovals.mockImplementation(async () => (mocks.approval ? [mocks.approval] : []));
    mocks.decide.mockResolvedValue({});
    mocks.cancelToolApproval.mockResolvedValue({});
    mocks.getActiveExecution.mockResolvedValue(null);
    mocks.stream.streamConversationId = null;
    mocks.stream.accumulatedContent = '';
    mocks.stream.streamDone = false;
    mocks.stream.streamApprovals = [];
		mocks.stream.streamFailure = null;
  });

  it('SSE 帧 status=waiting_approval 归一化为 pending（取消按钮判定依赖 pending）', async () => {
    mocks.approval = null;
    mocks.listAgents.mockResolvedValue([{ id: 'agent-1', name: 'Agent' }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);

    const { result, rerender } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.agents).toHaveLength(1));
    act(() => result.current.setSelectedAgent('agent-1'));
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));

    mocks.stream.streamConversationId = 'conversation-1';
    mocks.stream.streamApprovals = [{
      approvalId: 'approval-3',
      agentId: 'agent-1',
      toolName: 'delete',
      serverId: 'orders',
      riskLevel: 'destructive',
      status: 'waiting_approval', // SSE 帧实际下发的状态
    }];
    rerender();

    await waitFor(() => {
      const row = result.current.pendingApprovals.find((r) => r.approvalId === 'approval-3');
      expect(row?.status).toBe('pending');
      expect(row?.userId).toBe('test-user');
      expect(row?.riskLevel).toBe('destructive');
    });
  });

  it('SSE 帧与恢复行合并：已有字段优先、状态归一生效', async () => {
    // listToolApprovals 先返回完整行（含真实 riskLevel），SSE 帧后到不覆盖。
    mocks.approval = {
      approvalId: 'approval-4',
      agentId: 'agent-1',
      toolName: 'delete',
      serverId: 'orders',
      riskLevel: 'destructive',
      status: 'pending',
      conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'agent-1', name: 'Agent' }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);

    const { result, rerender } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.agents).toHaveLength(1));
    act(() => result.current.setSelectedAgent('agent-1'));
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));

    mocks.stream.streamConversationId = 'conversation-1';
    mocks.stream.streamApprovals = [{
      approvalId: 'approval-4',
      agentId: 'agent-1',
      toolName: 'delete',
      serverId: 'orders',
      riskLevel: 'unclassified', // SSE 帧占位值，不得覆盖已有 riskLevel
      status: 'waiting_approval',
    }];
    rerender();

    await waitFor(() => {
      const row = result.current.pendingApprovals.find((r) => r.approvalId === 'approval-4');
      expect(row?.status).toBe('pending');
      expect(row?.riskLevel).toBe('destructive');
      expect(row?.userId).toBe('test-user');
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
    mocks.stream.streamApprovals = [{
      approvalId: 'approval-2',
      agentId: 'agent-1',
      toolName: 'delete',
      serverId: 'orders',
      riskLevel: 'destructive',
      status: 'pending',
    }];
    rerender();

    await waitFor(() => {
      expect(result.current.messages.some((item) => item.content === '工具调用等待审批')).toBe(true);
    });
  });

  it('keeps API order and selects the first agent by default', async () => {
    mocks.listAgents.mockResolvedValue([
      { id: 'first', name: 'Agent A' },
      { id: 'second', name: 'Agent B' },
    ]);

    const { result } = renderHook(() => useChatPage());

    // 等化后无平台/普通之分：保持 API 列表顺序，默认选中首个。
    await waitFor(() => expect(result.current.agents.map((agent) => agent.id)).toEqual([
      'first', 'second',
    ]));
    expect(result.current.selectedAgent).toBe('first');
  });

  it('agents 存在时回退选择第一个 agent，会话列表照常加载', async () => {
    mocks.listAgents.mockResolvedValue([
      { id: 'regular-1', name: '普通 Agent A' },
      { id: 'regular-2', name: '普通 Agent B' },
    ]);
    mocks.listConversations.mockResolvedValue([{ id: 'conv-1', name: '会话' }]);

    const { result } = renderHook(() => useChatPage());

    // 不再回退到 null（null 会让 conversations effect 直接 return，会话列表永不加载）
    await waitFor(() => expect(result.current.selectedAgent).toBe('regular-1'));
    await waitFor(() => expect(result.current.selectedConv).toBe('conv-1'));
    expect(result.current.conversations).toHaveLength(1);
  });

  it('agents 为空时 selectedAgent 为 null 但加载态结束', async () => {
    mocks.listAgents.mockResolvedValue([]);

    const { result } = renderHook(() => useChatPage());

    await waitFor(() => expect(result.current.agentsLoading).toBe(false));
    expect(result.current.agents).toHaveLength(0);
    expect(result.current.selectedAgent).toBeNull();
  });

  it('hydrates streamed structured artifacts into the completed assistant message', async () => {
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手' }]);
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

	it('loads and creates conversations only for a fixed agent', async () => {
		const fixedID = 'stratum-platform-assistant';
		mocks.getAgent.mockResolvedValue({ id: fixedID, name: 'Stratum 平台助手' });
		const { result } = renderHook(() => useChatPage({ fixedAgentId: fixedID }));

		await waitFor(() => expect(result.current.selectedAgent).toBe(fixedID));
		expect(mocks.listAgents).not.toHaveBeenCalled();
		expect(mocks.getAgent).toHaveBeenCalledWith(fixedID);
		await act(async () => result.current.handleCreateConv());
		expect(mocks.createConversation).toHaveBeenCalledWith(fixedID);
	});

	it('copies noAnswer from the SSE done payload into the assistant message', async () => {
		mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手' }]);
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
		mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手' }]);
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
			approvals: [],
			delegateStatus: null,
			executionId: null,
			conflict: false,
		});

		const { result } = renderHook(() => useChatPage());
		await waitFor(() => expect(result.current.selectedAgent).toBe('system'));
		act(() => result.current.setSelectedAgent('system'));
		await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));

		await waitFor(() => {
			expect(result.current.messages.some((item) => item.noAnswer?.Reason === 'threshold_filtered')).toBe(true);
		});
	});


  // ── 审批取消 / 终态续跑（Task 71 前端 vitest 补全） ──
  // 轮询 effect 首次 poll() 同步触发（无需等待 ACTIVE_EXECUTION_POLL_MS interval）。

  it('auto-resumes with a cancelled placeholder after the approval is cancelled', async () => {
    mocks.approval = {
      approvalId: 'approval-1', agentId: 'system', toolName: 'delete', serverId: 'orders',
      riskLevel: 'destructive', status: 'pending', conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    mocks.getActiveExecution.mockResolvedValue({
      executionId: 'e1', agentId: 'system', userQuery: '执行删除', approvalId: 'approval-1',
      status: 'waiting_approval', approvalStatus: 'cancelled',
    });

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    // cancelled 终态续跑：占位气泡收敛为「已取消该工具，Agent 正在收尾…」，SSE 带 execution_id。
    // 占位气泡 content 可能由恢复路径先建（streamMsgIdRef 已占用），doApprovalResume
    // 的占位分支会跳过——以 SSE 续跑已触发（startStream 带 execution_id）为可靠信号；
    // 场景气泡文案由 cancelWaitingApproval 路径单独断言。
    await waitFor(() => {
      expect(mocks.stream.startStream).toHaveBeenCalledWith(
        'system',
        expect.objectContaining({ execution_id: 'e1' }),
      );
    });
  });

  it('auto-resumes with a rejection placeholder after the approval is rejected', async () => {
    mocks.approval = {
      approvalId: 'approval-1', agentId: 'system', toolName: 'delete', serverId: 'orders',
      riskLevel: 'destructive', status: 'pending', conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    mocks.getActiveExecution.mockResolvedValue({
      executionId: 'e1', agentId: 'system', userQuery: '执行删除', approvalId: 'approval-1',
      status: 'waiting_approval', approvalStatus: 'rejected',
    });

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    await waitFor(() => {
      expect(mocks.stream.startStream).toHaveBeenCalledWith(
        'system',
        expect.objectContaining({ execution_id: 'e1' }),
      );
    });
  });

  it('auto-resumes after an expired approval (过期纳入终态续跑)', async () => {
    mocks.approval = {
      approvalId: 'approval-1', agentId: 'system', toolName: 'delete', serverId: 'orders',
      riskLevel: 'destructive', status: 'pending', conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    mocks.getActiveExecution.mockResolvedValue({
      executionId: 'e1', agentId: 'system', userQuery: '执行删除', approvalId: 'approval-1',
      status: 'waiting_approval', approvalStatus: 'expired',
    });

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    await waitFor(() => {
      expect(result.current.pendingApprovals.find((r) => r.approvalId === 'approval-1')?.status).toBe('expired');
    });
    // 阶段一:expired 纳入终态续跑(修复过期卡死回归),LLM 感知工具未执行后自行收尾。
    await waitFor(() => {
      expect(mocks.stream.startStream).toHaveBeenCalledWith(
        'system',
        expect.objectContaining({ execution_id: 'e1' }),
      );
    });
  });

  it('cancels the pending approval then resumes with the active execution', async () => {
    mocks.approval = {
      approvalId: 'approval-1', agentId: 'system', toolName: 'delete', serverId: 'orders',
      riskLevel: 'destructive', status: 'pending', conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    // cancel 成功后 active-execution 反映 cancelled(后端置终态),整批全部已决 → 续跑;
    // 其余仍待决时不续跑、保留等待文案,轮询在全部终态后自动续跑。
    let approvalStatus = 'pending';
    mocks.cancelToolApproval.mockImplementation(async () => { approvalStatus = 'cancelled'; return {}; });
    mocks.getActiveExecution.mockImplementation(async () => ({
      executionId: 'e1', agentId: 'system', userQuery: '执行删除', approvalId: 'approval-1',
      status: 'waiting_approval', approvalStatus,
    }));

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    await waitFor(() => expect(result.current.waitingApprovals).toHaveLength(1));
    await act(async () => {
      await result.current.cancelWaitingApproval(mocks.approval!);
    });

    expect(mocks.cancelToolApproval).toHaveBeenCalledWith('approval-1');
    // 乐观置卡片终态 + 场景气泡。
    expect(result.current.pendingApprovals.find((r) => r.approvalId === 'approval-1')?.status).toBe('cancelled');
    expect(result.current.messages.some((m) => m.content === '已取消该工具，Agent 正在收尾…')).toBe(true);
    // 先取消、后续跑：顺序保证取消失败时不误续跑。
    expect(mocks.cancelToolApproval.mock.invocationCallOrder[0])
      .toBeLessThan(mocks.stream.startStream.mock.invocationCallOrder[0]);
    expect(mocks.stream.startStream).toHaveBeenCalledWith(
      'system',
      expect.objectContaining({ execution_id: 'e1' }),
    );
  });

  it('does not resume when cancelling races an already-decided approval', async () => {
    mocks.approval = {
      approvalId: 'approval-1', agentId: 'system', toolName: 'delete', serverId: 'orders',
      riskLevel: 'destructive', status: 'pending', conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    mocks.cancelToolApproval.mockRejectedValue({ status: 409 });

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    await waitFor(() => expect(result.current.waitingApprovals).toHaveLength(1));
    await act(async () => {
      await result.current.cancelWaitingApproval(mocks.approval!);
    });

    expect(mocks.cancelToolApproval).toHaveBeenCalledWith('approval-1');
    // 409：已被处理，不再乐观置终态、不续跑，给可解释 toast。
    expect(result.current.pendingApprovals.find((r) => r.approvalId === 'approval-1')?.status).toBe('pending');
    expect(mocks.stream.startStream).not.toHaveBeenCalled();
    expect(message.warning).toHaveBeenCalledWith(
      expect.objectContaining({ content: '该审批已被处理，无需再取消' }),
    );
  });

  it('switches to manual continue after two consecutive rejected terminals', async () => {
    mocks.approval = {
      approvalId: 'card-1', agentId: 'system', toolName: 'delete', serverId: 'orders',
      riskLevel: 'destructive', status: 'pending', conversationId: 'conversation-1',
    };
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手', isSystem: true }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    // 内存流存在（conversationId 命中）→ 恢复 effect 走内存流分支，不再消费
    // getActiveExecution；该 mock 只被轮询消费，调用序即轮询序。
    mocks.stream.getStreamState.mockReturnValue({
      streaming: false, conversationId: 'conversation-1', userQuery: '执行删除', content: '',
      done: false, result: null, error: null, approvals: [], delegateStatus: null, executionId: null, conflict: false,
    });
    // 卡片 approvalId 与 active approvalId 错配：poll 置终态分支按 active.approvalId
    // 匹配更新卡片 → 卡片保持 pending → waitingApprovals 存活 → 第二次轮询发生，
    // 连续 rejected 计数护栏（terminalResumeCountRef>=2）据此命中。
    let pollCalls = 0;
    mocks.getActiveExecution.mockImplementation(async () => {
      pollCalls += 1;
      return {
        executionId: pollCalls === 1 ? 'e1' : 'e2', agentId: 'system', userQuery: '执行删除',
        approvalId: 'active-1', status: 'waiting_approval', approvalStatus: 'rejected',
      };
    });

    const { result } = renderHook(() => useChatPage());
    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    // 第一次轮询（poll() 立即触发）→ 自动续跑。
    await waitFor(() => expect(mocks.stream.startStream).toHaveBeenCalledTimes(1));
    // 第二次轮询（ACTIVE_EXECUTION_POLL_MS=2s interval）→ 护栏置阻塞、转手动。
    await waitFor(() => expect(result.current.resumeBlocked).toBe(true), { timeout: 5000 });
    expect(result.current.resumeBlockedLabel).toBe('已多次审批未通过，是否让 Agent 继续？');
    expect(mocks.stream.startStream).toHaveBeenCalledTimes(1);
  });

  // 刷新恢复路径:setMessages 与 setLoadingMsgs(false) 被 await getActiveExecution 分隔成
  // 两批,第一批(loadingMsgs 仍 true)消息未挂载导致滚动无效——滚动 effect 需靠
  // loadingMsgs true→false 边沿补触发锚定。jsdom 的 rAF 由 Vitest pretendToBeVisual
  // 提供,waitFor 轮询可 flush 双 rAF。
  it('anchors only after loadingMsgs flips to false (refresh-style load)', async () => {
    mocks.listAgents.mockResolvedValue([{ id: 'system', name: '平台使用小助手' }]);
    mocks.listConversations.mockResolvedValue([{ id: 'conversation-1', name: 'Conversation' }]);
    mocks.getActiveExecution.mockResolvedValue(null);
    // clearAllMocks 保留实现：上一测试的 getStreamState.mockReturnValue 会泄漏到此，
    // 其 conversationId 命中当前会话会触发 stream-restore 分支多 append 一条消息。
    // 显式复位为无流态（conversationId null）走纯加载分支。
    mocks.stream.getStreamState.mockReturnValue({
      streaming: false, conversationId: null, userQuery: null, content: '',
      done: false, result: null, error: null, approvals: [], delegateStatus: null, executionId: null, conflict: false,
    });
    const msgs = [{ id: 'm1', role: 'assistant', content: 'hello' }] as unknown as ChatMessage[];
    vi.mocked(conversationApi.messages).mockResolvedValueOnce(msgs);

    const { result } = renderHook(() => useChatPage());
    // 记录每次滚动调用时 loadingMsgs 的状态：修复的核心是「loadingMsgs 仍 true 的
    // 消息批次不锚定」（消息未挂载、滚动无效），true→false 边沿才补触发。若锚定发生
    // 在 true 批次（旧实现行为），即是无效滚动——测试通过该断言区分修复是否生效。
    const loadingAtScroll: (boolean | undefined)[] = [];
    const scrollIntoView = vi.fn(() => {
      loadingAtScroll.push(result.current.loadingMsgs);
    });
    // 在加载 effect 触发前注入 fake bottomRef(滚动 effect 需 bottomRef 存在才锚定)。
    (result.current.bottomRef as MutableRefObject<HTMLDivElement>).current = {
      scrollIntoView,
    } as unknown as HTMLDivElement;

    await waitFor(() => expect(result.current.selectedConv).toBe('conversation-1'));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    await waitFor(() => expect(result.current.loadingMsgs).toBe(false));
    // 消息加载完成 + getActiveExecution(null) + setLoadingMsgs(false) 后,滚动 effect 以
    // true→false 边沿补触发锚定(双 rAF 由真实定时器触发)。
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalled());
    // 所有锚定必须发生在 loadingMsgs=false 之后;在 true 批次滚动(旧行为)即失败。
    expect(loadingAtScroll).not.toHaveLength(0);
    expect(loadingAtScroll.every((l) => l === false)).toBe(true);
  });


});

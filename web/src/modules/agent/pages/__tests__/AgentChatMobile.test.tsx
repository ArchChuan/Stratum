import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createRef } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AgentChatPage } from '../AgentChatPage';

const mocks = vi.hoisted(() => ({
  isMobile: true,
  setSelectedAgent: vi.fn(),
  setSelectedConv: vi.fn(),
  create: vi.fn(),
  rename: vi.fn(),
  remove: vi.fn(),
  send: vi.fn(),
	isAdmin: true,
	waitingApproval: null as null | Record<string, string>,
	streaming: false,
	manualResumeWaiting: vi.fn(),
	navigate: vi.fn(),
	streamFailure: null as null | { message: string; code?: string; status?: number },
	clearStreamFailure: vi.fn(),
  agents: [
    { id: 'agent-1', name: '移动 Agent', description: '测试', llmModel: 'gpt-test' },
    { id: 'agent-2', name: '备用 Agent' },
  ] as Array<Record<string, unknown>>,
  selectedAgent: 'agent-1',
}));

vi.mock('@/shared/hooks/useResponsive', () => ({
  useResponsive: () => ({ isMobile: mocks.isMobile, isCompact: mocks.isMobile }),
}));

vi.mock('react-router-dom', async () => ({
  ...(await vi.importActual<typeof import('react-router-dom')>('react-router-dom')),
  // 审批提示卡用 useNavigate 跳转审批中心;测试无 Router context,提供空实现。
  useNavigate: () => mocks.navigate,
}));

vi.mock('@/modules/iam', () => ({
  useTenantRole: () => ({ isAdmin: mocks.isAdmin }),
  // SourceCardList 经 knowledge barrel 间接引入 llmRoutes → PrivateRoute，
  // mock 边界需提供该导出（与 evaluation/routes.test.tsx 同款）
  PrivateRoute: ({ children }: { children: React.ReactNode }) => children,
}));

vi.mock('../../hooks/useChatPage', () => ({
  useChatPage: () => ({
    agents: mocks.agents,
    selectedAgent: mocks.selectedAgent,
    setSelectedAgent: mocks.setSelectedAgent,
    conversations: [
      { id: 'conv-1', name: '第一会话' },
      { id: 'conv-2', name: '第二会话' },
    ],
    loadingConvs: false,
    selectedConv: 'conv-1',
    setSelectedConv: mocks.setSelectedConv,
    messages: [{ id: 'message-1', role: 'assistant', content: `https://example.test/${'x'.repeat(200)}` }],
    loadingMsgs: false,
    sending: false,
    input: '你好',
    setInput: vi.fn(),
    bottomRef: createRef<HTMLDivElement>(),
    scrollContainerRef: createRef<HTMLDivElement>(),
    pinnedToBottomRef: { current: true },
    handleSend: mocks.send,
    handleCreateConv: mocks.create,
    handleRenameConv: mocks.rename,
    handleDeleteConv: mocks.remove,
	waitingApproval: mocks.waitingApproval,
		streaming: mocks.streaming,
		manualResumeWaiting: mocks.manualResumeWaiting,
		streamFailure: mocks.streamFailure,
		clearStreamFailure: mocks.clearStreamFailure,
  }),
}));

describe('AgentChatPage mobile layout', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.isMobile = true;
		mocks.isAdmin = true;
		mocks.waitingApproval = null;
		mocks.streaming = false;
		mocks.streamFailure = null;
		mocks.agents = [
      { id: 'agent-1', name: '移动 Agent', description: '测试', llmModel: 'gpt-test' },
      { id: 'agent-2', name: '备用 Agent' },
    ];
    mocks.selectedAgent = 'agent-1';
	});

  it('opens the conversation drawer and closes it after selecting a conversation', async () => {
    render(<AgentChatPage />);
    expect(screen.queryByText('新建会话')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }));
    expect(await screen.findByText('新建会话')).toBeInTheDocument();
    fireEvent.click(screen.getByText('第二会话'));
    expect(mocks.setSelectedConv).toHaveBeenCalledWith('conv-2');
    await waitFor(() => expect(screen.queryByText('新建会话')).not.toBeInTheDocument());
  });

  it('keeps agent and conversation operations available in the drawer', async () => {
    render(<AgentChatPage />);
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }));
    await screen.findByText('新建会话');
    fireEvent.mouseDown(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByText('备用 Agent'));
    expect(mocks.setSelectedAgent.mock.calls[0]?.[0]).toBe('agent-2');
    fireEvent.click(screen.getByRole('button', { name: '新建会话' }));
    expect(mocks.create).toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '重命名' }));
    const renameInput = screen.getByDisplayValue('第一会话');
    fireEvent.change(renameInput, { target: { value: '已重命名' } });
    fireEvent.keyDown(renameInput, { key: 'Enter' });
    await waitFor(() => expect(mocks.rename).toHaveBeenCalledWith('conv-1', '已重命名'));
    await waitFor(() => expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: '删除' }));
    await screen.findByText('删除此会话？');
    expect(screen.getByRole('dialog', { name: '会话列表' })).toBeInTheDocument();
    const confirmDelete = await waitFor(() => {
      const buttons = document.querySelectorAll<HTMLButtonElement>('.ant-popconfirm-buttons button');
      const button = buttons.item(buttons.length - 1);
      expect(button).not.toBeNull();
      return button!;
    });
    fireEvent.click(confirmDelete);
    expect(mocks.remove).toHaveBeenCalledWith('conv-1');
  });

  it('uses an accessible icon-only send button on phones', () => {
    render(<AgentChatPage />);
    const send = screen.getByRole('button', { name: '发送消息' });
    expect(send).not.toHaveTextContent('发送');
    fireEvent.click(send);
    expect(mocks.send).toHaveBeenCalled();
  });

  it('keeps the permanent sidebar and text send button on desktop', () => {
    mocks.isMobile = false;
    render(<AgentChatPage />);
    expect(screen.getByText('新建会话')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '打开会话列表' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '发送消息' })).toHaveTextContent('发送');
    mocks.isMobile = true;
  });

  it('closes an open drawer when transitioning to desktop', async () => {
    const view = render(<AgentChatPage />);
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }));
    expect(await screen.findByText('新建会话')).toBeInTheDocument();
    mocks.isMobile = false;
    view.rerender(<AgentChatPage />);
    await waitFor(() => expect(document.querySelector('.ant-drawer-open')).not.toBeInTheDocument());
    mocks.isMobile = true;
  });

  it('marks the chat surface and long content as overflow safe', () => {
    const { container } = render(<AgentChatPage />);
    expect(container.querySelector('.agent-chat-page')).toHaveStyle({ overflow: 'hidden' });
    expect(container.querySelector('.chat-message-list')).toHaveStyle({ padding: '12px' });
    expect(container.querySelector('.chat-message-bubble')).toHaveStyle({ maxWidth: '88%' });
    expect(container.querySelector('.chat-markdown')).toHaveStyle({ overflowWrap: 'anywhere' });
    expect(container.querySelector('.chat-composer')).toHaveStyle({
      paddingBottom: 'max(12px, env(safe-area-inset-bottom, 0px))',
    });
  });

  it('disables the streaming cursor animation when reduced motion is requested', () => {
    const { container } = render(<AgentChatPage />);
    expect(container.querySelector('style')?.textContent).toContain('prefers-reduced-motion: reduce');
  });

	it('shows a read-only approval notice with navigation to the approval center', () => {
		mocks.waitingApproval = {
			approvalId: 'approval-1', agentId: 'agent-1', toolName: 'delete',
			serverId: 'orders', riskLevel: 'destructive', status: 'pending',
		};
		render(<AgentChatPage />);
		// M3/M4:审批操作收敛到审批中心,对话页对所有人只读,不提供批准/拒绝按钮。
		expect(screen.getByText('工具 delete 等待审批')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '批准并继续' })).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '拒绝' })).not.toBeInTheDocument();
		expect(screen.getByRole('button', { name: '前往审批中心' })).toBeInTheDocument();
	});

	it('renders unknown outcomes as non-retryable reconciliation work', () => {
		mocks.waitingApproval = {
			approvalId: 'approval-1', agentId: 'agent-1', toolName: 'delete',
			serverId: 'orders', riskLevel: 'destructive', status: 'unknown_outcome',
		};
		render(<AgentChatPage />);
		expect(screen.getByText('工具执行结果未知，需要人工对账')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '批准并继续' })).not.toBeInTheDocument();
	});

	it.each(['cancelled', 'voided', 'invalidated'])(
		'renders %s as invalidated without approve controls',
		(status) => {
			mocks.waitingApproval = {
				approvalId: `approval-${status}`, agentId: 'agent-1', toolName: 'delete',
				serverId: 'orders', riskLevel: 'destructive', status,
			};
			render(<AgentChatPage />);
			expect(screen.getByText('工具审批已失效')).toBeInTheDocument();
			expect(screen.queryByRole('button', { name: '批准并继续' })).not.toBeInTheDocument();
		},
	);

	it('shows the mapped reason for conversation-delete invalidation', () => {
		mocks.waitingApproval = {
			approvalId: 'approval-voided', agentId: 'agent-1', toolName: 'delete',
			serverId: 'orders', riskLevel: 'destructive', status: 'voided',
			invalidationReason: 'conversation_deleted',
		};
		render(<AgentChatPage />);
		expect(screen.getByText('工具审批已失效：会话已删除')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '批准并继续' })).not.toBeInTheDocument();
	});

	it('renders authorization_denied as a terminal blocked state', () => {
		mocks.waitingApproval = {
			approvalId: 'approval-blocked', agentId: 'agent-1', toolName: 'delete',
			serverId: 'orders', riskLevel: 'destructive', status: 'authorization_denied',
		};
		render(<AgentChatPage />);
		expect(screen.getByText('权限已变更，工具执行已阻止')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '批准并继续' })).not.toBeInTheDocument();
	});

	it('renders an expired status as terminal without approve controls', () => {
		mocks.waitingApproval = {
			approvalId: 'approval-expired', agentId: 'agent-1', toolName: 'delete',
			serverId: 'orders', riskLevel: 'destructive', status: 'expired',
		};
		render(<AgentChatPage />);
		expect(screen.getByText('工具审批已过期')).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: '批准并继续' })).not.toBeInTheDocument();
	});

	it('prefers status-based invalidation over clock expiry', () => {
		// cancelled 的审批即使 expiresAt 已过，也应显示"已失效"而非"已过期"。
		mocks.waitingApproval = {
			approvalId: 'approval-invalidated', agentId: 'agent-1', toolName: 'delete',
			serverId: 'orders', riskLevel: 'destructive', status: 'cancelled',
			expiresAt: '2020-01-01T00:00:00Z', invalidationReason: 'conversation_deleted',
		};
		render(<AgentChatPage />);
		expect(screen.getByText('工具审批已失效：会话已删除')).toBeInTheDocument();
		expect(screen.queryByText('工具审批已过期')).not.toBeInTheDocument();
	});

  it('does not expose platform assistant model settings in chat', () => {
    mocks.agents = [{
      id: 'system', name: '平台使用小助手', description: '系统助手', llmModel: 'glm-5.2', isSystem: true,
    }];
    mocks.selectedAgent = 'system';
    const view = render(<AgentChatPage />);
    expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
    expect(screen.queryByText('助手设置')).not.toBeInTheDocument();

    mocks.isMobile = false;
    view.rerender(<AgentChatPage />);
    expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
  });

  it('keeps assistant model recovery out of chat for administrators', () => {
    mocks.isMobile = false;
    mocks.agents = [{
      id: 'system', name: '平台使用小助手', description: '系统助手', llmModel: '', isSystem: true,
    }];
    mocks.selectedAgent = 'system';
    mocks.streamFailure = {
      message: '租户尚未配置平台助手模型',
      code: 'SYSTEM_ASSISTANT_MODEL_UNAVAILABLE',
      status: 503,
    };

    render(<AgentChatPage />);

    expect(screen.getByText('租户尚未配置平台助手模型')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
    expect(screen.queryByText('助手设置')).not.toBeInTheDocument();
  });

  it('shows contact-admin guidance to members without a recovery action', () => {
    mocks.isMobile = false;
    mocks.isAdmin = false;
    mocks.agents = [{
      id: 'system', name: '平台使用小助手', description: '系统助手', llmModel: '', isSystem: true,
    }];
    mocks.selectedAgent = 'system';
    mocks.streamFailure = {
      message: '租户尚未配置平台助手模型',
      code: 'SYSTEM_ASSISTANT_MODEL_UNAVAILABLE',
      status: 503,
    };

    render(<AgentChatPage />);

    expect(screen.getByText('租户尚未配置平台助手模型，请联系租户管理员配置')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
  });

  it('does not offer model recovery for ordinary agents or unrelated errors', () => {
    mocks.isMobile = false;
    mocks.streamFailure = {
      message: '租户尚未配置平台助手模型',
      code: 'SYSTEM_ASSISTANT_MODEL_UNAVAILABLE',
      status: 503,
    };

    const view = render(<AgentChatPage />);
    expect(screen.queryByText('租户尚未配置平台助手模型，请联系租户管理员配置')).not.toBeInTheDocument();

    mocks.agents = [{
      id: 'system', name: '平台使用小助手', description: '系统助手', llmModel: '', isSystem: true,
    }];
    mocks.selectedAgent = 'system';
    mocks.streamFailure = { message: '服务暂时不可用', code: 'OTHER_FAILURE', status: 503 };
    view.rerender(<AgentChatPage />);

    expect(screen.queryByText('租户尚未配置平台助手模型')).not.toBeInTheDocument();
  });
});

import { createRef } from 'react';

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { AgentChatPage } from '../AgentChatPage';

const mocks = vi.hoisted(() => ({
  isMobile: true,
  setSelectedAgent: vi.fn(),
  setSelectedConv: vi.fn(),
  create: vi.fn(),
  rename: vi.fn(),
  remove: vi.fn(),
  send: vi.fn(),
}));

vi.mock('@/shared/hooks/useResponsive', () => ({
  useResponsive: () => ({ isMobile: mocks.isMobile, isCompact: mocks.isMobile }),
}));

vi.mock('../../hooks/useChatPage', () => ({
  useChatPage: () => ({
    agents: [
      { id: 'agent-1', name: '移动 Agent', description: '测试', llmModel: 'gpt-test' },
      { id: 'agent-2', name: '备用 Agent' },
    ],
    selectedAgent: 'agent-1',
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
  }),
}));

describe('AgentChatPage mobile layout', () => {
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
});

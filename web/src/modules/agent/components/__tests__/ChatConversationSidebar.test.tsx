import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { agentSchema } from '../../model/agent';
import { ChatConversationSidebar } from '../ChatConversationSidebar';

beforeAll(() =>
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })),
  ),
);

// agentSchema.parse 补全 Agent 必填字段（description/systemPrompt/llmModel 等），
// 避免测试只用部分对象导致 tsc --noEmit 类型错误（CI frontend typecheck 门禁）。
const baseProps = {
  agents: [
    agentSchema.parse({ id: 'agent-1', name: '普通 Agent' }),
    agentSchema.parse({ id: 'stratum-platform-assistant', name: '平台使用小助手' }),
  ],
  selectedAgent: 'stratum-platform-assistant',
  onSelectAgent: vi.fn(),
  conversations: [{ id: 'conv-1', name: '系统助手会话' }],
  loadingConvs: false,
  selectedConv: 'conv-1',
  onSelectConv: vi.fn(),
  onCreate: vi.fn(),
  onRename: vi.fn(),
  onDelete: vi.fn(),
};

describe('ChatConversationSidebar agents 加载失败降级', () => {
  it('agents 加载失败时显示错误态 + 重试按钮，不渲染空下拉', () => {
    const onRetry = vi.fn();
    render(<ChatConversationSidebar {...baseProps} agentsError onRetryAgents={onRetry} />);
    expect(screen.getByText('Agent 列表加载失败')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '重试加载 Agent' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
    // 错误态下不渲染 agent 下拉，避免空下拉误导（切换不了）
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('agents 加载失败时保留已有 selectedAgent 的会话列表（侧栏不整体空白）', () => {
    render(<ChatConversationSidebar {...baseProps} agentsError />);
    // 会话仍按 selectedAgent 正常展示
    expect(screen.getByText('系统助手会话')).toBeInTheDocument();
  });

  it('agents 正常时渲染 agent 下拉而非错误态', () => {
    render(<ChatConversationSidebar {...baseProps} />);
    expect(screen.queryByText('Agent 列表加载失败')).not.toBeInTheDocument();
    const combobox = screen.getByRole('combobox');
    expect(combobox).toBeInTheDocument();
    fireEvent.mouseDown(combobox);
    // 下拉展开且包含 agent 选项（选中值也会渲染同名文本，故直接断言 dropdown 内容）
    const dropdown = document.querySelector('.ant-select-dropdown:not(.ant-select-dropdown-hidden)');
    expect(dropdown?.textContent).toContain('平台使用小助手');
  });
});

describe('ChatConversationSidebar 无可用 Agent 空态', () => {
  it('agents 为空且无选中 agent 时显示「暂无可用 Agent」，而非空白', () => {
    render(
      <ChatConversationSidebar
        {...baseProps}
        agents={[]}
        selectedAgent={null}
        conversations={[]}
      />,
    );
    expect(screen.getByText('暂无可用 Agent')).toBeInTheDocument();
  });

  it('agents 加载中不显示「暂无可用 Agent」空态', () => {
    render(
      <ChatConversationSidebar
        {...baseProps}
        agents={[]}
        selectedAgent={null}
        conversations={[]}
        agentsLoading
      />,
    );
    expect(screen.queryByText('暂无可用 Agent')).not.toBeInTheDocument();
  });

  it('agents 加载失败时只显示错误态，不叠加「暂无可用 Agent」空态', () => {
    render(
      <ChatConversationSidebar
        {...baseProps}
        agents={[]}
        selectedAgent={null}
        conversations={[]}
        agentsError
      />,
    );
    expect(screen.getByText('Agent 列表加载失败')).toBeInTheDocument();
    expect(screen.queryByText('暂无可用 Agent')).not.toBeInTheDocument();
  });
});

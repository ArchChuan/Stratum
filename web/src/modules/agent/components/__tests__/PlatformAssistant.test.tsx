import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { agentApi } from '../../api/agent.api';
import type { Agent } from '../../model/agent';
import { ChatConversationSidebar } from '../ChatConversationSidebar';
import { ChatHeader } from '../ChatHeader';
import { DiagnosticReport } from '../DiagnosticReport';
import { SystemAssistantModelModal } from '../SystemAssistantModelModal';

vi.mock('../../api/agent.api', () => ({
  agentApi: {
    models: vi.fn(),
    getSystemSettings: vi.fn(),
    updateSystemSettings: vi.fn(),
  },
}));

const systemAgent: Agent = {
  id: 'stratum-platform-assistant',
  name: '平台使用小助手',
  description: '使用指导与租户状态诊断',
  type: 'react',
  systemPrompt: '',
  llmModel: '',
  allowedSkills: [],
  mcpToolIds: [],
  knowledgeWorkspaceIds: [],
  memoryScope: 'user',
  isSystem: true,
  managementMode: 'tenant_model_only',
};

const sidebarProps = {
  agents: [systemAgent],
  selectedAgent: systemAgent.id,
  onSelectAgent: vi.fn(),
  conversations: [],
  loadingConvs: false,
  selectedConv: null,
  onSelectConv: vi.fn(),
  onCreate: vi.fn(),
  onRename: vi.fn(),
  onDelete: vi.fn(),
};

describe('平台使用小助手界面', () => {
  beforeEach(() => vi.clearAllMocks());

  it('在选择器和标题中用 React 节点展示系统身份', async () => {
    render(<ChatConversationSidebar {...sidebarProps} />);
    expect(screen.getByText('系统内置')).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole('combobox'));
    await waitFor(() => {
      const option = document.querySelector('.ant-select-item-option-content');
      expect(option).toHaveTextContent('平台使用小助手');
      expect(option).toHaveTextContent('系统内置');
    });

    render(<ChatHeader agent={systemAgent} isAdmin onOpenSettings={vi.fn()} />);
    expect(screen.getAllByText('系统内置').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('button', { name: '设置助手模型' })).toBeInTheDocument();
  });

  it('成员在系统助手未配置模型时只看到联系管理员提示', () => {
    render(<ChatHeader agent={systemAgent} isAdmin={false} />);
    expect(screen.getByText('尚未配置模型，请联系租户管理员')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
  });

  it('系统助手已就绪时不显示未配置提示', () => {
    render(<ChatHeader agent={{ ...systemAgent, llmModel: 'tenant-model' }} isAdmin={false} />);
    expect(screen.queryByText(/尚未配置模型/)).not.toBeInTheDocument();
    expect(screen.getByText('tenant-model')).toBeInTheDocument();
  });

  it('模型设置弹窗只提交 llmModel 且不渲染其他配置字段', async () => {
    vi.mocked(agentApi.models).mockResolvedValue(['tenant-model', 'backup-model']);
    vi.mocked(agentApi.getSystemSettings).mockResolvedValue({
      agentId: systemAgent.id,
      llmModel: '',
      ready: false,
    });
    vi.mocked(agentApi.updateSystemSettings).mockResolvedValue({
      agentId: systemAgent.id,
      llmModel: 'tenant-model',
      ready: true,
    });
    const onSaved = vi.fn();
    render(<SystemAssistantModelModal open onClose={vi.fn()} onSaved={onSaved} />);

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getAllByRole('combobox')).toHaveLength(1);
    expect(within(dialog).queryByText(/Prompt|Skill|MCP|知识库|凭证/)).not.toBeInTheDocument();
    fireEvent.mouseDown(within(dialog).getByRole('combobox'));
    const modelOption = await waitFor(() => {
      const option = Array.from(document.querySelectorAll<HTMLElement>('.ant-select-item-option-content'))
        .find((item) => item.textContent === 'tenant-model');
      expect(option).toBeDefined();
      return option!;
    });
    fireEvent.click(modelOption);
    fireEvent.click(within(dialog).getByRole('button', { name: '保存模型' }));

    await waitFor(() => expect(agentApi.updateSystemSettings).toHaveBeenCalledWith({
      llmModel: 'tenant-model',
    }));
    expect(onSaved).toHaveBeenCalledWith('tenant-model');
  });

  it('弹窗关闭时不加载模型或设置', () => {
    render(<SystemAssistantModelModal open={false} onClose={vi.fn()} onSaved={vi.fn()} />);
    expect(agentApi.models).not.toHaveBeenCalled();
    expect(agentApi.getSystemSettings).not.toHaveBeenCalled();
  });

  it('把事实、缺口、建议、工具耗时和引用分区展示', () => {
    const { container } = render(<DiagnosticReport report={{
      facts: [{
        area: 'agent', statement: 'Agent 可正常读取', source: 'agent_repository',
        observedAt: '2026-07-23T12:00:00Z',
      }],
      inferences: ['当前配置满足基础使用条件'],
      evidenceGaps: [{ area: 'mcp', source: 'mcp_repository', code: 'evidence_timeout' }],
      recommendedActions: ['检查 MCP Server 连通性'],
      steps: [{ tool: 'stratum_diagnose_tenant', outcome: 'partial', latencyMs: 23 }],
      citations: [{
        documentId: 'agent-guide', title: 'Agent 使用指南', productVersion: 'v1',
        section: '模型配置', url: 'https://docs.example.test/agent', excerpt: '先配置租户模型。',
      }],
    }} />);

    expect(screen.getByText('诊断证据')).toBeInTheDocument();
    fireEvent.click(screen.getByText('诊断证据'));
    expect(screen.getByText('已确认事实')).toBeInTheDocument();
    expect(screen.getByText('证据缺口')).toBeInTheDocument();
    expect(screen.getByText('建议操作')).toBeInTheDocument();
    expect(screen.getByText('工具步骤与耗时')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Agent 使用指南/ })).toHaveAttribute(
      'href', 'https://docs.example.test/agent',
    );
    expect(screen.getByText('23 ms')).toBeInTheDocument();
    expect(container.querySelector('.diagnostic-report')).toHaveStyle({ minWidth: 0 });
    expect(container.querySelector('.diagnostic-report-content')).toHaveStyle({ overflowWrap: 'anywhere' });
  });
});

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Agent } from '../../model/agent';
import { ChatConversationSidebar } from '../ChatConversationSidebar';
import { ChatHeader } from '../ChatHeader';
import { DiagnosticReport } from '../DiagnosticReport';

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

  it('在选择器和标题中展示系统助手且不暴露模型设置', async () => {
    render(<ChatConversationSidebar {...sidebarProps} />);
    expect(screen.getByText('平台使用小助手')).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole('combobox'));
    await waitFor(() => {
      const option = document.querySelector('.ant-select-item-option-content');
      expect(option).toHaveTextContent('平台使用小助手');
    });

    render(<ChatHeader agent={systemAgent} isAdmin />);
    expect(screen.getAllByText('平台使用小助手').length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
    expect(screen.queryByText('助手设置')).not.toBeInTheDocument();
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

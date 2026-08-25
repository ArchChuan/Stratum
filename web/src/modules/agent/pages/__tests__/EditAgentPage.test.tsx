import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Form } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useEditAgentPage } from '../../hooks/useEditAgentPage';
import type { Agent } from '../../model/agent';
import { EditAgentPage } from '../EditAgentPage';

vi.mock('../../hooks/useEditAgentPage', () => ({ useEditAgentPage: vi.fn() }));
vi.mock('../../components/AgentFormSections', () => ({
  AgentFormSections: () => <div>普通 Agent 完整表单</div>,
}));

// F2：页面直接使用 useTenantRole（isAdmin 门控编辑/只读），测试 mock 返回值。
vi.mock('@/modules/iam', () => ({
  useTenantRole: () => ({ role: 'member', isAdmin: false, isOwner: false, isMember: true, hasTenantRole: () => false }),
}));
const { operationProposalApiMock } = vi.hoisted(() => ({
  operationProposalApiMock: { requestEditorAccess: vi.fn() },
}));
vi.mock('@/modules/operation-gate', () => ({ operationProposalApi: operationProposalApiMock }));

const baseHook = {
  loading: false,
  pageLoading: false,
  skills: [],
  mcpTools: [],
  workspaces: [],
  groupedModels: [],
  managementPath: '/agents',
  navigate: vi.fn(),
  onFinish: vi.fn(),
  readOnly: false,
  editorCandidates: [],
  editorCandidatesLoading: false,
};

const renderPage = (name: string, id: string, overrides: Partial<typeof baseHook> = {}) => {
  const agent: Agent = {
    id,
    name,
    description: '',
    type: 'react',
    systemPrompt: '',
    llmModel: '',
    allowedSkills: [],
    mcpToolIds: [],
    knowledgeWorkspaceIds: [],
    memoryScope: 'user',
  };
  const Harness = () => {
    const [form] = Form.useForm();
    vi.mocked(useEditAgentPage).mockReturnValue({
      ...baseHook,
      form,
      id,
      agent,
      ...overrides,
    } as ReturnType<typeof useEditAgentPage>);
    return <EditAgentPage />;
  };
  return render(<Harness />);
};

describe('EditAgentPage', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows the edit title and form for any agent', () => {
    renderPage('Stratum 平台助手', 'stratum-platform-assistant');

    // 等化后标题恒为「编辑 Agent」，不再区分平台助手与普通 Agent。
    expect(screen.getByText('编辑 Agent')).toBeInTheDocument();
    expect(screen.getByText('普通 Agent 完整表单')).toBeInTheDocument();
  });

  it('member 只读（非白名单）可点击「申请编辑权限」并提交 grant_editor 提案', async () => {
    renderPage('Stratum 平台助手', 'stratum-platform-assistant', { readOnly: true });

    // 按钮须在 Form 外可点：<Form disabled={readOnly}> 通过 DisabledContext 禁用表单内 Button。
    const requestBtn = await screen.findByRole('button', { name: /申请编辑权限/ });
    expect(requestBtn).toBeInTheDocument();
    expect(requestBtn.closest('form')).toBeNull();
    expect(screen.queryByRole('button', { name: /保存修改/ })).not.toBeInTheDocument();

    fireEvent.click(requestBtn);
    await waitFor(() => expect(operationProposalApiMock.requestEditorAccess).toHaveBeenCalledWith(
      'agent', 'stratum-platform-assistant', { resourceName: 'Stratum 平台助手' },
    ));
    expect(await screen.findByText('已提交，等待管理员审批')).toBeInTheDocument();
  });
});

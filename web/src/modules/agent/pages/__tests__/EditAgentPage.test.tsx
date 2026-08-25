import { render, screen } from '@testing-library/react';
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

const renderPage = (name: string, id: string) => {
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
});

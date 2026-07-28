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

const baseHook = {
  loading: false,
  pageLoading: false,
  skills: [],
  mcpTools: [],
  workspaces: [],
  navigate: vi.fn(),
  onFinish: vi.fn(),
};

const renderPage = (agentValues: Pick<Agent, 'isSystem' | 'name'>, id: string) => {
  const agent: Agent = {
    id,
    description: '',
    type: 'react',
    systemPrompt: '',
    llmModel: '',
    allowedSkills: [],
    mcpToolIds: [],
    knowledgeWorkspaceIds: [],
    memoryScope: 'user',
    ...agentValues,
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

  it('renders the unified form for the platform assistant', () => {
    renderPage({ isSystem: true, name: 'Stratum 平台助手' }, 'stratum-platform-assistant');

    expect(screen.getByText('平台助手设置')).toBeInTheDocument();
    expect(screen.getByText('普通 Agent 完整表单')).toBeInTheDocument();
  });

  it('retains the complete form for an ordinary Agent', () => {
    renderPage({ isSystem: false, name: '普通 Agent' }, 'agent-1');

    expect(screen.getByText('编辑 Agent')).toBeInTheDocument();
    expect(screen.getByText('普通 Agent 完整表单')).toBeInTheDocument();
  });
});

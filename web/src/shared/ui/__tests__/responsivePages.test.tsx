/* eslint-disable import/no-restricted-paths -- Cross-module page layout contract required by the responsive plan. */
import { readFileSync } from 'node:fs';

import { render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AgentCard } from '@/modules/agent/components/AgentCard';
import { AgentsListFilters } from '@/modules/agent/components/AgentsListFilters';
import { TenantEmbeddingCard } from '@/modules/iam/components/TenantEmbeddingCard';
import { LoginPage } from '@/modules/iam/pages/auth/LoginPage';
import { OnboardingPage } from '@/modules/iam/pages/auth/OnboardingPage';
import { WorkspaceCard } from '@/modules/knowledge/components/WorkspaceCard';
import { WorkspaceConfigForm } from '@/modules/knowledge/components/WorkspaceConfigForm';
import { WorkspaceCreateModal } from '@/modules/knowledge/components/WorkspaceCreateModal';
import { WorkspaceDetailHeader } from '@/modules/knowledge/components/WorkspaceDetailHeader';
import { WorkspaceListGrid } from '@/modules/knowledge/components/WorkspaceListGrid';
import { WorkspaceListHeader } from '@/modules/knowledge/components/WorkspaceListHeader';
import { SkillCard } from '@/modules/skill/components/SkillCard';
import { SkillFormSections } from '@/modules/skill/components/SkillFormSections';

vi.mock('@/modules/iam/components/AuthContext', () => ({
  useAuth: () => ({ login: vi.fn() }),
}));

const responsiveCss = readFileSync('src/index.css', 'utf8');

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

describe('responsive page contracts', () => {
  it('marks list headers and filters as stacking toolbars', () => {
    const { container, rerender } = render(
      <AgentsListFilters searchText="" onSearchChange={vi.fn()} onCreate={vi.fn()} />,
    );
    expect(container.firstElementChild).toHaveClass('responsive-toolbar');

    rerender(
      <WorkspaceListHeader
        searchText=""
        isAdmin
        onSearchChange={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    expect(container.firstElementChild).toHaveClass('responsive-page-header');
    expect(container.querySelector('.responsive-toolbar')).toBeInTheDocument();
  });

  it('uses a responsive grid for paired skill form controls', () => {
    const Fixture = () => {
      const [form] = Form.useForm();
      return (
        <Form form={form}>
          <SkillFormSections
            form={form}
            skillType="http"
            availableModels={['test-model']}
            modelsLoading={false}
            isEdit
          />
        </Form>
      );
    };
    const { container } = render(
      <Fixture />,
    );
    expect(container.querySelector('.responsive-form-grid')).toBeInTheDocument();
  });

  it('marks knowledge detail content as fluid and long-text safe', () => {
    const Fixture = () => {
      const [form] = Form.useForm();
      return (
        <>
          <WorkspaceDetailHeader
            name="workspace-with-a-very-long-identifier"
            description="https://example.test/a/very/long/path"
            onBack={vi.fn()}
          />
          <WorkspaceConfigForm form={form} loading={false} onSubmit={vi.fn()} />
        </>
      );
    };
    const { container } = render(
      <Fixture />,
    );
    expect(container.querySelector('.responsive-detail-header')).toBeInTheDocument();
    expect(container.querySelector('.responsive-inline-form')).toBeInTheDocument();
    expect(container.querySelector('.long-text')).toBeInTheDocument();
  });

  it('constrains authentication cards with viewport gutters', () => {
    const { container, rerender } = render(
      <MemoryRouter><LoginPage /></MemoryRouter>,
    );
    expect(container.querySelector('.auth-page')).toBeInTheDocument();
    const loginCard = container.querySelector<HTMLElement>('.auth-card');
    expect(loginCard).toHaveStyle({ width: '100%', maxWidth: '380px' });

    rerender(<MemoryRouter><OnboardingPage /></MemoryRouter>);
    expect(container.querySelector('.auth-page')).toBeInTheDocument();
    const onboardingCard = container.querySelector<HTMLElement>('.auth-card');
    expect(onboardingCard).toHaveStyle({ width: '100%', maxWidth: '440px' });
  });

  it('keeps tenant embedding controls fluid without widening them on desktop', () => {
    const { container } = render(
      <TenantEmbeddingCard
        embedModel=""
        fetchLoading={false}
        embedLoading={false}
        canEditKeys
        onSave={vi.fn()}
      />,
    );

    expect(container.querySelector('.tenant-embedding-card')).toBeInTheDocument();
    expect(container.querySelector('.tenant-embedding-controls')).toHaveStyle({ width: '100%' });
    expect(container.querySelector('.ant-select')).toHaveStyle({
      width: '100%',
      maxWidth: '300px',
    });
  });

  it('marks card grids and icon actions for narrow touch screens', () => {
    const { container } = render(
      <>
        <WorkspaceListGrid
          workspaces={[{
            name: 'docs',
            description: '',
            config: { embedding_model: '', chunking_strategy: 'structure_recursive' },
          }]}
          isAdmin
          searchText=""
          onOpen={vi.fn()}
          onDelete={vi.fn()}
          onCreate={vi.fn()}
        />
        <AgentCard
          canManage
          agent={{
            id: 'agent-1',
            name: 'Agent',
            description: '',
            type: 'react',
            systemPrompt: '',
            llmModel: '',
            allowedSkills: [],
            mcpServerIds: [],
            knowledgeWorkspaceIds: [],
            memoryScope: 'user',
          }}
          onExecute={vi.fn()}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
        />
        <SkillCard
          canManage
          skill={{ id: 'skill-1', name: 'Skill', description: '', type: 'code' }}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
        />
        <WorkspaceCard
          ws={{
            name: 'workspace',
            description: '',
            config: { embedding_model: '', chunking_strategy: 'structure_recursive' },
          }}
          isAdmin
          onOpen={vi.fn()}
          onDelete={vi.fn()}
        />
      </>,
    );

    expect(container.querySelector('.responsive-card-grid')).toBeInTheDocument();
    for (const name of ['执行 Agent', '编辑 Agent', '删除 Agent', '编辑技能', '删除技能', '查看知识库', '删除知识库']) {
      for (const button of screen.getAllByRole('button', { name })) {
        expect(button).toHaveClass('responsive-touch-target');
      }
    }
  });

  it('keeps the create-workspace modal within a phone viewport and scrolls its body', () => {
    const Fixture = () => {
      const [form] = Form.useForm();
      return (
        <WorkspaceCreateModal
          open
          loading={false}
          form={form}
          onClose={vi.fn()}
          onSubmit={vi.fn()}
        />
      );
    };
    render(<Fixture />);

    expect(document.querySelector('.mobile-overlay')).toBeInTheDocument();
    expect(document.querySelector('.ant-modal-body')).toHaveStyle({ overflowY: 'auto' });
  });

  it('constrains mobile overlay geometry and keeps the modal body scrollable', () => {
    expect(responsiveCss).toMatch(/\.mobile-overlay\.ant-modal[\s\S]*top:\s*max\(12px, env\(safe-area-inset-top, 0px\)\)/);
    expect(responsiveCss).toMatch(/\.mobile-overlay\.ant-modal[\s\S]*max-height:\s*calc\(100dvh[^;]+\)/);
    expect(responsiveCss).toMatch(/\.mobile-overlay \.ant-modal-content[\s\S]*display:\s*flex[\s\S]*flex-direction:\s*column/);
    expect(responsiveCss).toMatch(/\.mobile-overlay \.ant-modal-body[\s\S]*flex:\s*1[\s\S]*min-height:\s*0[\s\S]*overflow-y:\s*auto/);
  });
});

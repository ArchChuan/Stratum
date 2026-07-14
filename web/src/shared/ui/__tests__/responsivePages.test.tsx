/* eslint-disable import/no-restricted-paths -- Cross-module page layout contract required by the responsive plan. */
import { render } from '@testing-library/react';
import { Form } from 'antd';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { AgentsListFilters } from '@/modules/agent/components/AgentsListFilters';
import { LoginPage } from '@/modules/iam/pages/auth/LoginPage';
import { OnboardingPage } from '@/modules/iam/pages/auth/OnboardingPage';
import { WorkspaceConfigForm } from '@/modules/knowledge/components/WorkspaceConfigForm';
import { WorkspaceDetailHeader } from '@/modules/knowledge/components/WorkspaceDetailHeader';
import { WorkspaceListHeader } from '@/modules/knowledge/components/WorkspaceListHeader';
import { SkillFormSections } from '@/modules/skill/components/SkillFormSections';

vi.mock('@/modules/iam/components/AuthContext', () => ({
  useAuth: () => ({ login: vi.fn() }),
}));

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
    expect(container.querySelector('.auth-card')).toBeInTheDocument();

    rerender(<MemoryRouter><OnboardingPage /></MemoryRouter>);
    expect(container.querySelector('.auth-page')).toBeInTheDocument();
    expect(container.querySelector('.auth-card')).toBeInTheDocument();
  });
});

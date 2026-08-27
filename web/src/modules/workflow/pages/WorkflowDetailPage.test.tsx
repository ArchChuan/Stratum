import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { WorkflowDetailPage } from './WorkflowDetailPage';

const role = vi.hoisted(() => ({ isAdmin: false }));

vi.mock('@/modules/iam', () => ({ useTenantRole: () => role }));
vi.mock('../api/workflow.api', () => ({
  workflowApi: {
    getWorkflow: vi.fn(),
    listWorkflowVersions: vi.fn(),
    getWorkflowVersion: vi.fn(),
    rollbackWorkflow: vi.fn(),
  },
}));
vi.mock('../components/WorkflowReadonlyCanvas', () => ({
  WorkflowReadonlyCanvas: () => <section aria-label="工作流详情图">只读图</section>,
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })));
});

const definition = {
  id: 'workflow-1', name: '客户研究', description: '形成研究摘要', revision: 3,
  active_version_id: 'v2', spec: { nodes: [], edges: [], max_concurrency: 0 },
  input_schema: { task_label: '研究主题', task_description: '', fields: [] },
  created_at: '2026-07-23T00:00:00Z', updated_at: '2026-07-24T00:00:00Z',
};
const versionsPage = {
  versions: [
    { id: 'v2', definition_id: 'workflow-1', version: 2, name: '稳定版', description: '', created_at: '2026-07-24T00:00:00Z' },
    { id: 'v1', definition_id: 'workflow-1', version: 1, name: '初版', description: '', created_at: '2026-07-23T00:00:00Z' },
  ],
  total: 2, page: 1, page_size: 100,
};
const activeVersion = {
  id: 'v2', definition_id: 'workflow-1', version: 2, name: '稳定版', description: '',
  spec: { nodes: [], edges: [], max_concurrency: 0 },
  input_schema: { task_label: '研究主题', task_description: '', fields: [] },
  created_at: '2026-07-24T00:00:00Z',
};

const renderPage = () => render(
  <MemoryRouter initialEntries={['/workflows/workflow-1']}><Routes>
    <Route path="/workflows/:id" element={<WorkflowDetailPage />} />
  </Routes></MemoryRouter>,
);

describe('WorkflowDetailPage', () => {
  beforeEach(() => {
    role.isAdmin = false;
    vi.clearAllMocks();
    vi.mocked(workflowApi.getWorkflow).mockResolvedValue(definition);
    vi.mocked(workflowApi.listWorkflowVersions).mockResolvedValue(versionsPage);
    vi.mocked(workflowApi.getWorkflowVersion).mockResolvedValue(activeVersion);
  });

  it('shows members the active workflow detail without rollback actions', async () => {
    renderPage();
    expect(await screen.findByRole('region', { name: '工作流详情图' })).toBeInTheDocument();
    expect(screen.getByText('客户研究')).toBeInTheDocument();
    expect(screen.getByText('生效版本 v2')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '运行工作流' })).toBeInTheDocument();
    // member 只能看生效版本与历史，看不到回退入口。
    expect(screen.queryByRole('button', { name: /回\s*滚/ })).not.toBeInTheDocument();
  });

  it('lets admins roll back to a historical version after confirmation and reloads', async () => {
    role.isAdmin = true;
    vi.mocked(workflowApi.rollbackWorkflow).mockResolvedValue(definition);
    renderPage();
    await screen.findByRole('region', { name: '工作流详情图' });
    // v2 是当前生效版本，仅 v1 行渲染回退按钮。
    fireEvent.click(screen.getByRole('button', { name: /回\s*滚/ }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: /回\s*滚/ }));

    await waitFor(() => expect(workflowApi.rollbackWorkflow).toHaveBeenCalledWith('workflow-1', 'v1'));
    // 回退后重新拉取定义与生效版本。
    await waitFor(() => expect(vi.mocked(workflowApi.getWorkflow).mock.calls.length).toBeGreaterThan(1));
  });
});

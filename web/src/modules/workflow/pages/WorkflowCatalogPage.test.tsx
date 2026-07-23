import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkflowCatalogPage } from './WorkflowCatalogPage';

import { workflowApi } from '../api/workflow.api';

const role = vi.hoisted(() => ({ isAdmin: false }));
const navigate = vi.hoisted(() => vi.fn());

vi.mock('@/modules/iam', () => ({ useTenantRole: () => role }));
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});
vi.mock('../api/workflow.api', () => ({
  workflowApi: { listWorkflows: vi.fn() },
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })));
});

describe('WorkflowCatalogPage', () => {
  beforeEach(() => {
    role.isAdmin = false;
    vi.clearAllMocks();
    vi.mocked(workflowApi.listWorkflows).mockResolvedValue({
      workflows: [{ id: 'workflow-1', name: '客户研究', description: '形成研究摘要', revision: 2, updated_at: '2026-07-23T00:00:00Z' }],
      total: 21,
      page: 1,
      page_size: 20,
    });
  });

  it('shows the catalog to members without exposing authoring actions', async () => {
    render(<MemoryRouter><WorkflowCatalogPage /></MemoryRouter>);
    expect(await screen.findByText('客户研究')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '新建工作流' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '运行工作流' }));
    expect(navigate).toHaveBeenCalledWith('/workflows/workflow-1/run');
  });

  it('lets admins create and edit workflows', async () => {
    role.isAdmin = true;
    render(<MemoryRouter><WorkflowCatalogPage /></MemoryRouter>);
    expect(await screen.findByRole('button', { name: '新建工作流' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '编辑草稿' }));
    expect(navigate).toHaveBeenCalledWith('/workflows/workflow-1/edit');
  });

  it('forwards search and server pagination and renders approved empty copy', async () => {
    vi.mocked(workflowApi.listWorkflows)
      .mockResolvedValueOnce({ workflows: [], total: 0, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ workflows: [], total: 0, page: 1, page_size: 20 });
    render(<MemoryRouter><WorkflowCatalogPage /></MemoryRouter>);
    expect(await screen.findByText('工作流还是空的')).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText('搜索工作流'), { target: { value: '不存在' } });
    await waitFor(() => expect(workflowApi.listWorkflows).toHaveBeenLastCalledWith({ query: '不存在', page: 1, pageSize: 20 }));
    expect(await screen.findByText('没有找到匹配的工作流')).toBeInTheDocument();
  });
});

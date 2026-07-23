import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { WorkflowRunsPage } from './WorkflowRunsPage';

const role = vi.hoisted(() => ({ isAdmin: false }));
vi.mock('@/modules/iam', () => ({ useTenantRole: () => role }));
vi.mock('../api/workflow.api', () => ({ workflowApi: { listWorkflowRuns: vi.fn() } }));
beforeAll(() => vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }))));

describe('WorkflowRunsPage', () => {
  beforeEach(() => {
    role.isAdmin = false;
    vi.clearAllMocks();
    vi.mocked(workflowApi.listWorkflowRuns).mockResolvedValue({ runs: [], total: 0, page: 1, page_size: 20 });
  });
  it('shows members only their run-center semantics and forwards server filters', async () => {
    render(<MemoryRouter><WorkflowRunsPage /></MemoryRouter>);
    expect(await screen.findByText('我的运行')).toBeInTheDocument();
    expect(screen.queryByLabelText('运行范围')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('工作流标识'), { target: { value: 'workflow-1' } });
    await waitFor(() => expect(workflowApi.listWorkflowRuns).toHaveBeenLastCalledWith({ definitionId: 'workflow-1', status: '', page: 1, pageSize: 20 }));
    expect(await screen.findByText('没有找到匹配的运行')).toBeInTheDocument();
  });
  it('lets admins express the all-runs scope without client ownership filtering', async () => {
    role.isAdmin = true;
    render(<MemoryRouter><WorkflowRunsPage /></MemoryRouter>);
    expect(await screen.findByText('运行中心')).toBeInTheDocument();
    expect(screen.getByLabelText('运行范围')).toBeInTheDocument();
    expect(workflowApi.listWorkflowRuns).toHaveBeenCalledWith({ definitionId: '', status: '', page: 1, pageSize: 20 });
  });
});

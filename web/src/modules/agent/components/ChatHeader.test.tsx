import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { ChatHeader } from './ChatHeader';

import { workflowApi } from '@/modules/workflow/api/workflow.api';

const navigate = vi.hoisted(() => vi.fn());
vi.mock('react-router-dom', async () => ({ ...(await vi.importActual<typeof import('react-router-dom')>('react-router-dom')), useNavigate: () => navigate }));
vi.mock('@/modules/workflow/api/workflow.api', () => ({ workflowApi: { listWorkflows: vi.fn() } }));
beforeAll(() => vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }))));

describe('ChatHeader workflow shortcut', () => {
  it('loads searchable workflows and navigates to the dedicated execution page', async () => {
    vi.mocked(workflowApi.listWorkflows).mockResolvedValue({ workflows: [{ id: 'workflow-1', name: '客户研究', description: '', revision: 1, updated_at: '' }], total: 1, page: 1, page_size: 50 });
    render(<MemoryRouter><ChatHeader /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: /运行固定工作流/ }));
    await waitFor(() => expect(workflowApi.listWorkflows).toHaveBeenCalled());
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '选择固定工作流' }));
    expect(await screen.findByText('客户研究')).toBeInTheDocument();
    fireEvent.click(screen.getByText('客户研究'));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/workflows/workflow-1/run'));
  });
});

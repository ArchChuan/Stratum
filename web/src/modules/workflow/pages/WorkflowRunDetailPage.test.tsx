import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { WorkflowRunDetailPage } from './WorkflowRunDetailPage';

const state = vi.hoisted(() => ({ value: null as any }));
vi.mock('../hooks/useWorkflowRunStream', () => ({ useWorkflowRunStream: () => state.value }));
vi.mock('@/shared/hooks', () => ({ useResponsive: () => ({ isMobile: true }) }));
beforeAll(() => vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }))));

describe('WorkflowRunDetailPage', () => {
  it('renders only actions allowed by the backend and uses a mobile step list', () => {
    state.value = {
      run: { id: 'run-1', status: 'running', generation: 2, snapshot: { nodes: [{ id: 'n1', type: 'approval', name: '确认' }], edges: [] }, output: '' },
      attempts: [{ node_id: 'n1', status: 'running' }], approvals: [], effectIntents: [], availableActions: ['cancel'], lastSequence: 0, outputByNode: {}, toolStepsByNode: {}, connection: 'connected',
    };
    render(<MemoryRouter initialEntries={['/workflow-runs/run-1']}><Routes><Route path="/workflow-runs/:runId" element={<WorkflowRunDetailPage />} /></Routes></MemoryRouter>);
    expect(screen.getByText('确认')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '取消运行' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '暂停' })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: '工作流运行图' })).not.toBeInTheDocument();
  });
});

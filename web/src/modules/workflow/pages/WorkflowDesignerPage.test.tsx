import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { WorkflowDesignerPage } from './WorkflowDesignerPage';

const responsive = vi.hoisted(() => ({ isMobile: true }));
vi.mock('@/shared/hooks', () => ({ useResponsive: () => responsive }));
vi.mock('../hooks/useWorkflowDesigner', () => ({ useWorkflowDesigner: () => ({
  editor: { spec: { nodes: [], edges: [], max_concurrency: 0 }, positions: {}, selected: null, dirty: false },
  dispatch: vi.fn(), definition: null, name: '', description: '', inputSchema: { task_label: '', task_description: '', fields: [] },
  setName: vi.fn(), setDescription: vi.fn(), setInputSchema: vi.fn(), loading: false, saving: false, validating: false,
  publishing: false, dirty: false, validatedRevision: null, save: vi.fn(), validate: vi.fn(), publish: vi.fn(),
}) }));
vi.mock('../hooks/useWorkflowResources', () => ({ useWorkflowResources: () => ({
  agents: [], skills: [], skillRevisions: [], mcpServers: [],
}) }));

describe('WorkflowDesignerPage', () => {
  it('shows a desktop-required gate on mobile without mounting the canvas', () => {
    render(<MemoryRouter initialEntries={['/workflows/new']}><Routes><Route path="/workflows/new" element={<WorkflowDesignerPage />} /></Routes></MemoryRouter>);
    expect(screen.getByText('请使用桌面端编辑工作流')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: '工作流画布' })).not.toBeInTheDocument();
  });
});

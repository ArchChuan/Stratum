import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { workflowApi } from '../api/workflow.api';

import { WorkflowVersionPage } from './WorkflowVersionPage';

vi.mock('../api/workflow.api', () => ({ workflowApi: { getWorkflowVersion: vi.fn() } }));
vi.mock('../components/WorkflowReadonlyCanvas', () => ({ WorkflowReadonlyCanvas: () => <section aria-label="工作流版本图">只读图</section> }));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
});

describe('WorkflowVersionPage', () => {
  it('renders an immutable graph and input schema without authoring actions', async () => {
    vi.mocked(workflowApi.getWorkflowVersion).mockResolvedValue({
      id: 'version-1', definition_id: 'workflow-1', version: 2, name: '研究流程', description: '稳定版本',
      spec: { nodes: [], edges: [], max_concurrency: 0 },
      input_schema: { task_label: '研究主题', task_description: '', fields: [{ key: 'region', label: '地区', type: 'single_select', required: true, description: '', options: [] }] },
      created_at: '2026-07-23T00:00:00Z',
    });
    render(<MemoryRouter initialEntries={['/workflows/workflow-1/versions/version-1']}><Routes>
      <Route path="/workflows/:id/versions/:versionId" element={<WorkflowVersionPage />} />
    </Routes></MemoryRouter>);
    expect(await screen.findByRole('region', { name: '工作流版本图' })).toBeInTheDocument();
    expect(screen.getByText('研究主题')).toBeInTheDocument();
    expect(screen.getByText('地区')).toBeInTheDocument();
    for (const action of ['保存草稿', '校验工作流', '发布工作流', '节点工具箱']) {
      expect(screen.queryByRole('button', { name: action })).not.toBeInTheDocument();
    }
  });
});

import { render, screen, within } from '@testing-library/react';
import { beforeEach, vi } from 'vitest';

import { RuntimeHealthTrendPanel } from './RuntimeHealthTrendPanel';

const mocks = vi.hoisted(() => ({
  listResources: vi.fn(),
  listRuns: vi.fn(),
}));
vi.mock('../api/evaluation.api', () => ({
  evaluationApi: { listResources: mocks.listResources, listRuns: mocks.listRuns },
}));

const run = (id: string, createdAt: string, passed: boolean, passedCases: number, totalCases: number, revision: string) => ({
  id, resource_id: 'agent-1', revision_id: revision, status: 'succeeded', resource_kind: 'agent',
  passed, total_cases: totalCases, passed_cases: passedCases, created_at: createdAt,
});

describe('RuntimeHealthTrendPanel', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.listResources.mockResolvedValue({ items: [{ id: 'agent-1', resource_id: 'agent-1', resource_kind: 'agent',
      status: 'active', created_at: '2026-08-01T00:00:00Z' }] });
  });

  it('loads runs for the default resource and renders the health trend', async () => {
    mocks.listRuns.mockResolvedValue({ items: [
      run('run-1', '2026-08-01T02:00:00Z', false, 6, 10, 'rev-a'),
      run('run-2', '2026-08-05T02:00:00Z', true, 8, 10, 'rev-b'),
      run('run-3', '2026-08-09T02:00:00Z', true, 9, 10, 'rev-b'),
    ] });
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);

    expect(await screen.findByText(/运行\s*3\s*次/)).toBeInTheDocument();
    expect(screen.getByText(/平均健康分/)).toBeInTheDocument();
    expect(screen.getByText(/76\.7%/)).toBeInTheDocument();
    expect(screen.getByTestId('health-trend-chart')).toBeInTheDocument();

    const table = await screen.findByRole('table');
    expect(within(table).getByText('run-1')).toBeInTheDocument();
    expect(within(table).getByText('90.0%')).toBeInTheDocument();
  });

  it('renders an empty hint when the resource has no run records', async () => {
    mocks.listRuns.mockResolvedValue({ items: [] });
    render(<RuntimeHealthTrendPanel defaultKind="agent" defaultResourceId="agent-1" />);

    expect(await screen.findByText('该资源尚无评测运行记录')).toBeInTheDocument();
    expect(mocks.listRuns).toHaveBeenCalledWith({ resource_kind: 'agent', resource_id: 'agent-1', limit: 100 });
  });
});

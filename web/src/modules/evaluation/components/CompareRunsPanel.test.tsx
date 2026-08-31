import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { RunSummary } from '../model/evaluation';

import { CompareRunsPanel } from './CompareRunsPanel';

const runs: RunSummary[] = [
  { id: 'r-v1', resource_id: 's1', revision_id: 'v1', status: 'succeeded', resource_kind: 'skill', passed: true, total_cases: 2, passed_cases: 2, created_at: '2026-08-30T00:00:00Z' },
  { id: 'r-v2', resource_id: 's1', revision_id: 'v2', status: 'succeeded', resource_kind: 'skill', passed: false, total_cases: 2, passed_cases: 1, created_at: '2026-08-31T00:00:00Z' },
  { id: 'r-v3', resource_id: 's2', revision_id: 'v3', status: 'succeeded', resource_kind: 'skill', passed: true, total_cases: 2, passed_cases: 2, created_at: '2026-08-30T00:00:00Z' },
];

const getRun = vi.fn().mockImplementation(async (id: string) => ({
  id,
  resource: { kind: 'skill', resource_id: 's1', revision_id: id === 'r-v2' ? 'v2' : 'v1' },
  suite_revision_id: 'rev-1', passed: true, total_cases: 2, passed_cases: 1, created_at: '',
  metrics: {
    by_dimension: {
      faithfulness: { avg_score: id === 'r-v2' ? 0.6 : 0.8, pass_rate: id === 'r-v2' ? 0.5 : 1, samples: 2 },
    },
  },
  results: [],
}));

describe('CompareRunsPanel', () => {
  it('compares by_dimension between two runs of the same resource', async () => {
    render(<CompareRunsPanel currentId="r-v2" runs={runs} getRun={getRun} />);
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '对比目标' }));
    fireEvent.click(await screen.findByText(/v1（/));
    await waitFor(() => expect(screen.getByText('faithfulness')).toBeInTheDocument());
    expect(screen.getByText('-0.200')).toBeInTheDocument(); // 0.6 - 0.8
  });

  it('excludes runs of other resources from the base-run candidates', async () => {
    render(<CompareRunsPanel currentId="r-v2" runs={runs} getRun={getRun} />);
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '对比目标' }));
    // Same-resource run r-v1 remains selectable as the baseline.
    expect(await screen.findByText(/v1（/)).toBeInTheDocument();
    // r-v3 belongs to resource s2, so it must not appear in the dropdown.
    expect(screen.queryByText(/v3/)).toBeNull();
  });
});

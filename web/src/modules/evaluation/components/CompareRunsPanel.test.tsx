import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { RunSummary } from '../model/evaluation';

import { CompareRunsPanel } from './CompareRunsPanel';

const runs: RunSummary[] = [
  { id: 'r-v1', resource_id: 's1', revision_id: 'v1', status: 'succeeded', resource_kind: 'skill', passed: true, total_cases: 2, passed_cases: 2, created_at: '2026-08-30T00:00:00Z' },
  { id: 'r-v2', resource_id: 's1', revision_id: 'v2', status: 'succeeded', resource_kind: 'skill', passed: false, total_cases: 2, passed_cases: 1, created_at: '2026-08-31T00:00:00Z' },
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
});

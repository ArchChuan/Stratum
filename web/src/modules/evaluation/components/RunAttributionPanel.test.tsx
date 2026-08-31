import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { EvaluationRun } from '../model/evaluation';

import { RunAttributionPanel } from './RunAttributionPanel';

const results: EvaluationRun['results'] = [
  { case_id: 'c1', passed: false, failure_reason: 'dimension:faithfulness',
    trace_id: 't-1', actual: '回复不准确', tokens: 0, cost_usd: 0, duration_ms: 0,
    dimensions: [{ name: 'faithfulness', score: 0.3, passed: false, confidence: 0.7 }] },
  { case_id: 'c2', passed: false, failure_reason: 'dimension:faithfulness',
    tokens: 0, cost_usd: 0, duration_ms: 0,
    dimensions: [{ name: 'faithfulness', score: 0.4, passed: false, confidence: 0.6 }] },
  { case_id: 'c3', passed: true, tokens: 0, cost_usd: 0, duration_ms: 0 },
];

describe('RunAttributionPanel', () => {
  it('clusters failed cases by failure_reason and drills into one case', () => {
    render(<RunAttributionPanel results={results} />);
    expect(screen.getByText('dimension:faithfulness')).toBeInTheDocument();
    expect(screen.getByText('2 个失败用例')).toBeInTheDocument();
    fireEvent.click(screen.getByText('c1'));
    expect(screen.getByText('faithfulness')).toBeInTheDocument();
    expect(screen.getByText('t-1')).toBeInTheDocument();
    expect(screen.queryByText('c3')).not.toBeInTheDocument();
  });
});

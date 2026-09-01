import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { RunMetricPanel } from './RunMetricPanel';

describe('RunMetricPanel', () => {
  it('renders scalar, by_dimension, version, cost and latency sections', () => {
    const metrics = {
      overall_pass_rate: 0.5,
      by_dimension: {
        faithfulness: { avg_score: 0.6, pass_rate: 0.5, samples: 2 },
        relevance: { avg_score: 0.8, pass_rate: 1, samples: 2 },
      },
      cost: { total_usd: 0.04, avg_usd: 0.02 },
      latency: { p50_ms: 100, p95_ms: 300, max_ms: 300 },
      version: { suite_revision_id: 'rev-1', platform_seq: 3, resource_version: 'res-v2' },
    };
    render(<RunMetricPanel metrics={metrics} />);
    expect(screen.getByText('基础指标')).toBeInTheDocument();
    expect(screen.getByText('总体通过率')).toBeInTheDocument();
    expect(screen.getByText('语义维度')).toBeInTheDocument();
    expect(screen.getByText('faithfulness')).toBeInTheDocument();
    expect(screen.getByText('relevance')).toBeInTheDocument();
    expect(screen.getByText('版本锚点')).toBeInTheDocument();
    expect(screen.getByText('rev-1')).toBeInTheDocument();
  });

  it('labels process_pass_rate as a percentage', () => {
    render(<RunMetricPanel metrics={{ overall_pass_rate: 0.5, process_pass_rate: 0.8 }} />);
    expect(screen.getByText('过程通过率')).toBeInTheDocument();
    expect(screen.getByText('80.0%')).toBeInTheDocument();
  });
});

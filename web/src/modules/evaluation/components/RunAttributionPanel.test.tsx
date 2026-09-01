import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { EvaluationRun } from '../model/evaluation';

import { RunAttributionPanel } from './RunAttributionPanel';

const results: EvaluationRun['results'] = [
  { case_id: 'c1', passed: false, process_pass: true, failure_reason: 'dimension:faithfulness',
    trace_id: 't-1', actual: '回复不准确', tokens: 0, cost_usd: 0, duration_ms: 0,
    dimensions: [{ name: 'faithfulness', score: 0.3, passed: false, confidence: 0.7 }],
    trace_evidence: { cost_usd: 0.05, latency_ms: 200, success: false, tool_call_count: 3, tool_error_count: 1 } },
  { case_id: 'c2', passed: false, process_pass: true, failure_reason: 'dimension:faithfulness',
    tokens: 0, cost_usd: 0, duration_ms: 0,
    dimensions: [{ name: 'faithfulness', score: 0.4, passed: false, confidence: 0.6 }] },
  { case_id: 'c3', passed: true, process_pass: true, tokens: 0, cost_usd: 0, duration_ms: 0 },
  { case_id: 'c4', passed: false, process_pass: false, process_failure: 'process:must_not_call:delete',
    failure_reason: 'process:must_not_call:delete',
    tools: [{ tool_name: 'delete', tool_type: 'mcp', step_index: 2, provider_type: 'zhipu',
      capability_id: 'cap-1', arguments: { query: 'secret' }, raw_text: '删除一行' }],
    tokens: 0, cost_usd: 0, duration_ms: 0 },
];

describe('RunAttributionPanel', () => {
  it('clusters failed cases by failure_reason and drills into one case', () => {
    render(<RunAttributionPanel results={results} />);
    expect(screen.getByText('dimension:faithfulness')).toBeInTheDocument();
    expect(screen.getByText('2 个失败用例')).toBeInTheDocument();
    fireEvent.click(screen.getByText('c1'));
    expect(screen.getByText('faithfulness')).toBeInTheDocument();
    expect(screen.getByText('t-1')).toBeInTheDocument();
    expect(screen.getByText('工具调用')).toBeInTheDocument();
    expect(screen.getByText('Trace 延迟 (ms)')).toBeInTheDocument();
    expect(screen.getByText('200')).toBeInTheDocument();
    expect(screen.getByText('输出未通过')).toBeInTheDocument();
    expect(screen.getByText('过程通过')).toBeInTheDocument();
    expect(screen.queryByText('c3')).not.toBeInTheDocument();
  });

  it('shows the process failure and tool sequence in the drill-down', () => {
    render(<RunAttributionPanel results={results} />);
    fireEvent.click(screen.getByText('c4'));
    expect(screen.getByText('过程未通过')).toBeInTheDocument();
    expect(screen.getByText('过程失败')).toBeInTheDocument();
    expect(screen.getAllByText('process:must_not_call:delete').length).toBeGreaterThan(0);
    expect(screen.getByText('工具序列')).toBeInTheDocument();
    expect(screen.getByText('delete')).toBeInTheDocument();
    expect(screen.getByText('zhipu')).toBeInTheDocument();
  });

  it('hides the trace evidence block when a case has none', () => {
    render(<RunAttributionPanel results={results} />);
    fireEvent.click(screen.getByText('c2'));
    expect(screen.getByText('用例 c2')).toBeInTheDocument();
    expect(screen.queryByText('工具调用')).not.toBeInTheDocument();
  });
});

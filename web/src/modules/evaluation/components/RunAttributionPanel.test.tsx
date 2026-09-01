import { fireEvent, render, screen, within } from '@testing-library/react';
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
  // c4：过程失败但输出断言通过——后端不产生 failure_reason（过程归因单独在
  // ProcessFailure，spec §6.5），必须仍进入失败聚类并可 drill-down。
  { case_id: 'c4', passed: false, process_pass: false, process_failure: 'process:must_not_call:delete',
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

  it('clusters process-fail-only cases by process_failure and drills into the tool sequence', () => {
    render(<RunAttributionPanel results={results} />);
    // c4 无 failure_reason 但 process_pass=false：现按 process_failure 归因进入失败聚类。
    expect(screen.getByText('1 个失败用例')).toBeInTheDocument();
    expect(screen.getAllByText('process:must_not_call:delete')).toHaveLength(1);

    fireEvent.click(screen.getByText('c4'));
    expect(screen.getByText('过程未通过')).toBeInTheDocument();
    expect(screen.getByText('过程失败')).toBeInTheDocument();
    // drill-down 内「失败归因」（process_failure fallback）+「过程失败」各展示一次。
    const drillDown = within(screen.getByTestId('case-drill-down'));
    expect(drillDown.getAllByText('process:must_not_call:delete')).toHaveLength(2);
    // 全局精确计数：聚类行 1 + drill-down 2。
    expect(screen.getAllByText('process:must_not_call:delete')).toHaveLength(3);
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

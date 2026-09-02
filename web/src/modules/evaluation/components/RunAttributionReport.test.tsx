import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { EvaluationRun } from '../model/evaluation';

import { RunAttributionPanel } from './RunAttributionPanel';
import {
  buildReportRows,
  hypothesisFor,
  rootCausePlaceholder,
  RunAttributionReport,
  suggestionsFor,
} from './RunAttributionReport';

const results: EvaluationRun['results'] = [
  // c1/c2：同维度 faithfulness 批量失分（输出断言失败），c1 带 trace 失败证据。
  { case_id: 'c1', passed: false, process_pass: true, failure_reason: 'dimension:faithfulness',
    trace_id: 't-1', actual: '回复不准确', tokens: 0, cost_usd: 0, duration_ms: 0,
    dimensions: [{ name: 'faithfulness', score: 0.3, passed: false, confidence: 0.7 }],
    trace_evidence: { cost_usd: 0.05, latency_ms: 200, success: false, tool_call_count: 3, tool_error_count: 1 } },
  { case_id: 'c2', passed: false, process_pass: true, failure_reason: 'dimension:faithfulness',
    tokens: 0, cost_usd: 0, duration_ms: 0,
    dimensions: [{ name: 'faithfulness', score: 0.4, passed: false, confidence: 0.6 }] },
  { case_id: 'c3', passed: true, process_pass: true, tokens: 0, cost_usd: 0, duration_ms: 0 },
  // c4：过程断言失败且无维度打分（过程归因在 ProcessFailure，spec §6.5）。
  { case_id: 'c4', passed: false, process_pass: false, process_failure: 'process:must_not_call:delete',
    tokens: 0, cost_usd: 0, duration_ms: 0 },
];

describe('buildReportRows', () => {
  it('aggregates failed cases by failing dimension with reasons and case ids', () => {
    const rows = buildReportRows(results);
    expect(rows).toHaveLength(2);

    const faithfulness = rows.find((row) => row.dimension === 'faithfulness');
    expect(faithfulness).toMatchObject({
      count: 2,
      reasons: ['dimension:faithfulness'],
      caseIds: ['c1', 'c2'],
      systematic: true,
      hasProcessFailure: false,
      hasTraceFailure: true,
    });
    expect(faithfulness?.avgScore).toBeCloseTo(0.35, 5);

    const process = rows.find((row) => row.dimension === '过程断言');
    expect(process).toMatchObject({
      count: 1,
      reasons: ['process:must_not_call:delete'],
      caseIds: ['c4'],
      systematic: false,
      hasProcessFailure: true,
      hasTraceFailure: false,
    });
    expect(process?.avgScore).toBeNull();
  });

  it('excludes passed cases and returns an empty list when nothing failed', () => {
    expect(buildReportRows([{ case_id: 'c3', passed: true, process_pass: true, tokens: 0, cost_usd: 0, duration_ms: 0 }]))
      .toHaveLength(0);
  });
});

describe('hypothesisFor / suggestionsFor', () => {
  const systematic = buildReportRows(results).find((row) => row.dimension === 'faithfulness')!;
  const isolated = buildReportRows(results).find((row) => row.dimension === '过程断言')!;

  it('marks the root-cause slot as pending §9 and never invents a cause', () => {
    // 中性表述：不编造「失分」因果（过程断言桶聚合多个 case 时无维度失分语义），
    // 只描述「失败 case 共享该归因维度」的证据模式（系统性/孤立由 UI 模式列 Tag 表达）。
    expect(hypothesisFor(systematic)).toContain('共享该归因维度');
    expect(hypothesisFor(systematic)).toContain(rootCausePlaceholder);
    expect(hypothesisFor(isolated)).toContain('单 case 归因该维度');
    expect(hypothesisFor(isolated)).toContain(rootCausePlaceholder);
  });

  it('gives at least one data-triggered suggestion per row', () => {
    for (const row of [systematic, isolated]) {
      expect(suggestionsFor(row).length).toBeGreaterThanOrEqual(1);
    }
    // trace 失败证据 → 工具下钻；过程断言失败 → 评审池；基线受控对比恒提供。
    expect(suggestionsFor(systematic).join(' ')).toContain('下钻 trace 证据');
    expect(suggestionsFor(isolated).join(' ')).toContain('评审池');
    for (const row of [systematic, isolated]) {
      expect(suggestionsFor(row).join(' ')).toContain('运行受控对比（§6.3①）');
    }
  });
});

describe('RunAttributionReport', () => {
  it('renders dimension rows and drills into a case via onSelectCase', () => {
    const onSelectCase = vi.fn();
    render(<RunAttributionReport results={results} onSelectCase={onSelectCase} />);
    const report = within(screen.getByTestId('run-attribution-report'));
    expect(report.getByText('faithfulness')).toBeInTheDocument();
    expect(report.getByText('过程断言')).toBeInTheDocument();
    // 根因假设位是全句文本（如「...根因假设待 §9 归因服务」），需按子串正则断言。
    expect(report.getAllByText(/待 §9 归因服务/).length).toBeGreaterThanOrEqual(2);

    fireEvent.click(report.getByText('c4'));
    expect(onSelectCase).toHaveBeenCalledWith('c4');
  });

  it('shows an empty state when the run has no failed cases', () => {
    render(<RunAttributionReport results={[{ case_id: 'c3', passed: true, process_pass: true, tokens: 0, cost_usd: 0, duration_ms: 0 }]} />);
    expect(screen.getByText(/无失败 case/)).toBeInTheDocument();
  });
});

describe('RunAttributionPanel integration', () => {
  it('reveals the failure attribution report behind the toggle without regressing clusters', () => {
    render(<RunAttributionPanel results={results} />);
    // 既有失败聚类主路径原样可用，报告默认收起不干扰其断言。
    expect(screen.getByText('dimension:faithfulness')).toBeInTheDocument();
    expect(screen.queryByTestId('run-attribution-report')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '查看失败归因报告' }));
    const report = within(screen.getByTestId('run-attribution-report'));
    expect(report.getByText('失败归因报告')).toBeInTheDocument();
    expect(report.getByText('faithfulness')).toBeInTheDocument();
    expect(report.getAllByText(/运行受控对比（§6.3①）/).length).toBeGreaterThanOrEqual(1);

    // 报告内点击 case id 走既有 CaseDrillDown。
    fireEvent.click(report.getByText('c1'));
    expect(screen.getByText('输出未通过')).toBeInTheDocument();
  });
});

describe('buildReportRows core invariants', () => {
  type RunResult = EvaluationRun['results'][number];

  // 局部 fixture helper：显式带 schema 必填字段（passed/process_pass/tokens/cost/duration），
  // 只覆写用例相关字段，避免污染文件级共享 results fixture。
  const failed = (over: { case_id: string } & Partial<RunResult>): RunResult => ({
    passed: false,
    process_pass: true,
    tokens: 0,
    cost_usd: 0,
    duration_ms: 0,
    ...over,
  });

  it('counts a multi-failed-dimension case once per failing dimension with its own score', () => {
    const rows = buildReportRows([
      failed({
        case_id: 'm1',
        failure_reason: 'dimension:faithfulness',
        dimensions: [
          { name: 'faithfulness', score: 0.2, passed: false },
          { name: 'relevance', score: 0.9, passed: false },
        ],
      }),
      failed({
        case_id: 'f1',
        failure_reason: 'dimension:faithfulness',
        dimensions: [{ name: 'faithfulness', score: 0.5, passed: false }],
      }),
    ]);

    // 一个 case 命中两个失败维度 → 各维度行各计入一次。
    expect(rows).toHaveLength(2);
    const faith = rows.find((row) => row.dimension === 'faithfulness');
    const relevance = rows.find((row) => row.dimension === 'relevance');
    expect(faith).toBeDefined();
    expect(relevance).toBeDefined();
    expect(faith).toMatchObject({ count: 2, caseIds: ['f1', 'm1'] });
    expect(faith?.avgScore).toBeCloseTo(0.35, 5); // (0.2 + 0.5) / 2
    expect(relevance).toMatchObject({ count: 1, caseIds: ['m1'] });
    expect(relevance?.avgScore).toBeCloseTo(0.9, 5);
  });

  it.each([
    {
      name: 'parses the dimension: prefix from failure_reason when no dimension scores exist',
      input: failed({ case_id: 'b1', failure_reason: 'dimension:faithfulness | assert:regex' }),
      expectDimension: 'faithfulness',
      expectReasons: ['dimension:faithfulness | assert:regex'],
    },
    {
      name: 'buckets un-prefixed failures into the 未标注 fallback dimension',
      input: failed({ case_id: 't1', failure_reason: 'timeout' }),
      expectDimension: '未标注',
      expectReasons: ['timeout'],
    },
    {
      name: 'falls back to the process:failed key when process_pass=false without a specific process_failure',
      input: failed({ case_id: 'p1', process_pass: false }),
      expectDimension: '过程断言',
      expectReasons: ['process:failed'],
      expectProcessFailure: true,
    },
  ])('$name', ({ input, expectDimension, expectReasons, expectProcessFailure }) => {
    const rows = buildReportRows([input]);
    expect(rows).toHaveLength(1);
    expect(rows[0].dimension).toBe(expectDimension);
    expect(rows[0].reasons).toEqual(expectReasons);
    if (expectProcessFailure !== undefined) {
      expect(rows[0].hasProcessFailure).toBe(expectProcessFailure);
    }
  });
});

import { Empty, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo } from 'react';

import type { EvaluationRun } from '../model/evaluation';

type RunResult = EvaluationRun['results'][number];

export type ReportRow = {
  dimension: string;
  count: number;
  avgScore: number | null;
  reasons: string[];
  caseIds: string[];
  systematic: boolean;
  hasProcessFailure: boolean;
  hasTraceFailure: boolean;
};

// 失败 case 是否进入归因报告：与失败聚类同一套口径——输出断言失败
// （failure_reason）或过程断言失败（process_pass=false）都算可归因失败。
const isFailedAttributionCase = (r: RunResult): boolean =>
  !r.passed && (Boolean(r.failure_reason) || r.process_pass === false);

// reasonOf 复用失败聚类的归因键：输出断言失败取 failure_reason，过程失败取
// process_failure，过程失败但无具体值时给兜底标签。
const reasonOf = (r: RunResult): string =>
  r.failure_reason || (r.process_pass === false ? (r.process_failure || 'process:failed') : '');

// failingDimensions 只消费后端已有证据，不做归因推断：优先取维度打分里
// passed=false 的维度名；无打分维度时回退解析 failure_reason 的 dimension:
// 前缀（spec §6.3 归因串形如 "dimension:faithfulness | assert:..."）；过程断言
// 失败且无维度归因归为「过程断言」；其余兜底「未标注」。
const failingDimensions = (r: RunResult): string[] => {
  const failed = (r.dimensions ?? []).filter((d) => d.passed === false).map((d) => d.name);
  if (failed.length > 0) {
    return [...new Set(failed)];
  }
  const match = /dimension:([^\s|]+)/.exec(r.failure_reason ?? '');
  if (match) {
    return [match[1]];
  }
  if (r.process_pass === false) {
    return ['过程断言'];
  }
  return ['未标注'];
};

const scoreFor = (r: RunResult, dimension: string): number | null => {
  const score = (r.dimensions ?? []).find((d) => d.name === dimension);
  return score == null ? null : score.score;
};

const isTraceEvidenceFailure = (r: RunResult): boolean => {
  const evidence = r.trace_evidence;
  return Boolean(evidence && (evidence.success === false || (evidence.tool_error_count ?? 0) > 0));
};

// buildReportRows 把失败 case 按维度聚合：一个失败 case 可能同时命中多个失败
// 维度，因此在每个命中维度下各计入一次（count = 该维度去重后的失败 case 数）。
export const buildReportRows = (results: EvaluationRun['results']): ReportRow[] => {
  const byDimension = new Map<string, RunResult[]>();
  for (const r of results) {
    if (!isFailedAttributionCase(r)) {
      continue;
    }
    for (const dimension of failingDimensions(r)) {
      const items = byDimension.get(dimension) ?? [];
      items.push(r);
      byDimension.set(dimension, items);
    }
  }
  return [...byDimension.entries()]
    .map(([dimension, items]) => {
      const scores = items
        .map((r) => scoreFor(r, dimension))
        .filter((s): s is number => s != null);
      return {
        dimension,
        count: items.length,
        avgScore: scores.length > 0 ? scores.reduce((a, b) => a + b, 0) / scores.length : null,
        reasons: [...new Set(items.map(reasonOf))].filter(Boolean).sort(),
        caseIds: [...new Set(items.map((r) => r.case_id))].sort(),
        systematic: items.length >= 2,
        hasProcessFailure: items.some((r) => r.process_pass === false),
        hasTraceFailure: items.some(isTraceEvidenceFailure),
      };
    })
    .sort((a, b) => b.count - a.count || a.dimension.localeCompare(b.dimension));
};

// 根因假设位：本视图不臆测根因（不写「faithfulness 批量失分→检索或 prompt 问题」
// 这类推断），只标注证据模式并把根因假设留给 §9 归因服务（卡 D）。
export const rootCausePlaceholder = '待 §9 归因服务';

export const hypothesisFor = (row: ReportRow): string =>
  row.systematic
    ? `系统性批量失分：${row.count} 个失败 case 同维度；根因假设${rootCausePlaceholder}`
    : `单 case 失分；根因假设${rootCausePlaceholder}`;

// suggestionsFor 只产出数据可触发的确定性动作，映射既有能力而非编造：trace 证据
// 失败→下钻工具/错误序列；过程断言失败→评审池（§6.6）；任何失败都给受控对比方向
// （§6.3① 同集 V1/V2）。保证每个归因行至少一个建议方向。
export const suggestionsFor = (row: ReportRow): string[] => {
  const suggestions: string[] = [];
  if (row.hasTraceFailure) {
    suggestions.push('下钻 trace 证据：定位工具错误与失败步骤（工具序列/错误计数）');
  }
  if (row.hasProcessFailure) {
    suggestions.push('核查过程断言失败：进入评审池处理（§6.6）');
  }
  suggestions.push('运行受控对比（§6.3①）：在「版本对比」中选择基线运行，对照该维度差异');
  return suggestions;
};

const columns: ColumnsType<ReportRow> = [
  { title: '维度', dataIndex: 'dimension', key: 'dimension' },
  { title: '失败 case 数', dataIndex: 'count', key: 'count' },
  {
    title: '失败项平均分',
    dataIndex: 'avgScore',
    key: 'avgScore',
    render: (v: number | null) => (v == null ? '-' : v.toFixed(3)),
  },
  {
    title: '失败类型',
    dataIndex: 'reasons',
    key: 'reasons',
    render: (reasons: string[]) => (reasons.length > 0
      ? <Space size={[4, 4]} wrap>{reasons.map((reason) => <Tag key={reason}>{reason}</Tag>)}</Space>
      : '-'),
  },
  {
    title: '模式',
    dataIndex: 'systematic',
    key: 'systematic',
    render: (systematic: boolean) => (systematic ? <Tag color="warning">系统性</Tag> : <Tag>孤立</Tag>),
  },
  {
    title: '根因假设',
    key: 'hypothesis',
    render: (_: unknown, row: ReportRow) => hypothesisFor(row),
  },
  {
    title: '建议方向',
    key: 'suggestions',
    render: (_: unknown, row: ReportRow) => (
      <ul style={{ margin: 0, paddingInlineStart: 16 }}>
        {suggestionsFor(row).map((suggestion) => <li key={suggestion}>{suggestion}</li>)}
      </ul>
    ),
  },
];

// RunAttributionReport implements spec §6.3 ④ 失败归因报告（视图层）：把本运行
// 失败 case 聚合成维度行——哪个维度失分、哪些 case 聚类、系统性/孤立模式——根因
// 假设位显式挂起待 §9 归因服务（不臆测），建议方向为确定性动作，全部只消费现有
// run results，不依赖后端归因端点。
export const RunAttributionReport = ({ results, onSelectCase }: {
  results: EvaluationRun['results'];
  onSelectCase?: (caseId: string) => void;
}) => {
  const rows = useMemo(() => buildReportRows(results), [results]);

  return (
    <div data-testid="run-attribution-report">
      <Typography.Title level={5}>失败归因报告</Typography.Title>
      <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
        按失败维度聚合本运行的失败 case；根因假设由 §9 归因服务生成（当前为占位，视图不臆测根因）；
        建议方向为确定性动作，对应既有受控对比 / 工具下钻 / 评审池能力。
      </Typography.Paragraph>
      {rows.length === 0
        ? <Empty description="该运行无失败 case，无需归因报告" />
        : <Table<ReportRow>
          rowKey="dimension"
          size="small"
          pagination={false}
          dataSource={rows}
          columns={columns}
          expandable={{
            defaultExpandAllRows: true,
            expandedRowRender: (row) => (
              <div>
                {row.caseIds.map((id) => (onSelectCase
                  ? <a key={id} role="button" onClick={() => onSelectCase(id)} style={{ marginRight: 12 }}>{id}</a>
                  : <span key={id} style={{ marginRight: 12 }}>{id}</span>))}
              </div>
            ),
          }}
        />}
    </div>
  );
};

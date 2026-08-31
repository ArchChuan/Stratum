import { Descriptions, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

// metricLabels maps the legacy top-level scalar run metrics keys (eval_runs.metrics
// keys produced by the backend, kept for backward compatibility) to Chinese labels;
// unknown scalar keys fall back to the raw key.
const metricLabels: Record<string, string> = {
  pass_rate: '通过率',
  overall_pass_rate: '总体通过率',
  total_cases: '用例数',
  total_tokens: '总 tokens',
  total_cost_usd: '总成本 (USD)',
  avg_tokens_per_case: '平均 tokens/用例',
  avg_latency_ms: '平均延迟 (ms)',
  p95_latency_ms: 'P95 延迟 (ms)',
  avg_recall_at_5: '平均 Recall@5',
  avg_precision_at_5: '平均 Precision@5',
  avg_mrr: '平均 MRR',
  avg_ndcg_at_5: '平均 nDCG@5',
  rag_case_count: 'RAG 证据用例数',
};

function formatMetric(key: string, value: number): string {
  if (key === 'pass_rate' || key === 'overall_pass_rate') {
    return `${(value * 100).toFixed(1)}%`;
  }
  if (Number.isInteger(value)) {
    return String(value);
  }
  return value.toFixed(4);
}

type DimensionRow = {
  name: string;
  avg_score: number;
  pass_rate: number;
  samples: number;
};

const dimensionColumns: ColumnsType<DimensionRow> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '平均分', dataIndex: 'avg_score', key: 'avg_score', render: (v: number) => v.toFixed(3) },
  { title: '通过率', dataIndex: 'pass_rate', key: 'pass_rate', render: (v: number) => `${(v * 100).toFixed(1)}%` },
  { title: '样本数', dataIndex: 'samples', key: 'samples' },
];

const versionLabels: Record<string, string> = {
  suite_revision_id: '套件版本', platform_seq: '平台参数序号', resource_version: '资源版本',
};

// RunMetricPanel renders the spec §6.2 multidimensional run metrics: legacy
// scalar top-level keys + overall_pass_rate as a base section, by_dimension as
// a table, and version/cost/latency as compact sections. Only top-level number
// values pass through formatMetric, so nested objects can never render as
// "[object Object]".
export const RunMetricPanel = ({ metrics }: { metrics: Record<string, unknown> }) => {
  const dimensions = metrics.by_dimension as Record<string, { avg_score: number; pass_rate: number; samples: number }> | undefined;
  const version = metrics.version as Record<string, unknown> | undefined;
  const cost = metrics.cost as Record<string, number> | undefined;
  const latency = metrics.latency as Record<string, number> | undefined;

  const scalars = Object.entries(metrics).filter((entry): entry is [string, number] => typeof entry[1] === 'number');
  const rows: DimensionRow[] = dimensions ? Object.entries(dimensions).map(([name, d]) => ({
    name, avg_score: d.avg_score, pass_rate: d.pass_rate, samples: d.samples,
  })) : [];

  return (
    <div data-testid="run-metric-panel">
      {scalars.length > 0 && <Typography.Title level={5}>基础指标</Typography.Title>}
      {scalars.length > 0 && <Descriptions bordered size="small" column={1}>
        {scalars.map(([key, value]) => (
          <Descriptions.Item key={key} label={metricLabels[key] ?? key}>{formatMetric(key, value)}</Descriptions.Item>
        ))}
      </Descriptions>}
      <Typography.Title level={5}>语义维度</Typography.Title>
      <Table<DimensionRow>
        rowKey="name"
        size="small"
        pagination={false}
        dataSource={rows}
        columns={dimensionColumns}
        locale={{ emptyText: '无 judge 维度数据' }}
      />
      {version && <Typography.Title level={5}>版本锚点</Typography.Title>}
      {version && <Descriptions bordered size="small" column={1}>
        {Object.entries(version).map(([key, value]) => (
          <Descriptions.Item key={key} label={versionLabels[key] ?? key}>{String(value ?? '')}</Descriptions.Item>
        ))}
      </Descriptions>}
      {(cost || latency) && <Typography.Title level={5}>成本与延迟</Typography.Title>}
      {cost && <Descriptions bordered size="small" column={1}>
        <Descriptions.Item label="总成本 (USD)">{cost.total_usd?.toFixed(4)}</Descriptions.Item>
        <Descriptions.Item label="平均成本 (USD)">{cost.avg_usd?.toFixed(4)}</Descriptions.Item>
      </Descriptions>}
      {latency && Object.keys(latency).length > 0 && <Descriptions bordered size="small" column={1}>
        <Descriptions.Item label="P50 延迟 (ms)">{latency.p50_ms}</Descriptions.Item>
        <Descriptions.Item label="P95 延迟 (ms)">{latency.p95_ms}</Descriptions.Item>
        <Descriptions.Item label="最大延迟 (ms)">{latency.max_ms}</Descriptions.Item>
      </Descriptions>}
    </div>
  );
};

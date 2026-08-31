import { Descriptions, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo, useState } from 'react';

import type { DimensionScore, EvaluationRun } from '../model/evaluation';

type ClusterRow = { key: string; reason: string; count: number; failedCaseIds: string[] };

// RunAttributionPanel implements spec §6.3 case-clustering attribution: failed
// cases grouped by failure_reason, with per-cluster count and a single-case
// drill-down showing dimension scores, trace id and actual output.
export const RunAttributionPanel = ({ results }: { results: EvaluationRun['results'] }) => {
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const clusters = useMemo<ClusterRow[]>(() => {
    const map = new Map<string, { count: number; ids: string[] }>();
    for (const r of results) {
      if (r.passed || !r.failure_reason) {
        continue;
      }
      const entry = map.get(r.failure_reason) ?? { count: 0, ids: [] };
      entry.count += 1;
      entry.ids.push(r.case_id);
      map.set(r.failure_reason, entry);
    }
    return [...map.entries()].map(([reason, e]) => ({
      key: reason, reason, count: e.count, failedCaseIds: e.ids,
    }));
  }, [results]);

  const columns: ColumnsType<ClusterRow> = [
    { title: '失败归因', dataIndex: 'reason', key: 'reason' },
    { title: '失败用例', dataIndex: 'count', key: 'count', render: (v: number) => `${v} 个失败用例` },
  ];

  const selected = results.find((r) => r.case_id === selectedId && !r.passed);

  return (
    <div data-testid="run-attribution-panel">
      <Typography.Title level={5}>失败聚类</Typography.Title>
      <Table<ClusterRow>
        rowKey="key"
        size="small"
        pagination={false}
        dataSource={clusters}
        columns={columns}
        expandable={{
          defaultExpandAllRows: true,
          expandedRowRender: (row) => (
            <div>
              {row.failedCaseIds.map((id) => (
                <a key={id} role="button" onClick={() => setSelectedId(id)} style={{ marginRight: 12 }}>{id}</a>
              ))}
            </div>
          ),
        }}
        locale={{ emptyText: '没有失败用例' }}
      />
      {selected && <CaseDrillDown result={selected} />}
    </div>
  );
};

const CaseDrillDown = ({ result }: { result: NonNullable<EvaluationRun['results'][number]> }) => (
  <div data-testid="case-drill-down">
    <Typography.Title level={5}>用例 {result.case_id}</Typography.Title>
    {result.dimensions && result.dimensions.length > 0 && (
      <Table<DimensionScore>
        rowKey="name"
        size="small"
        pagination={false}
        dataSource={result.dimensions}
        columns={dimensionScoreColumns}
      />
    )}
    <Descriptions bordered size="small" column={1}>
      <Descriptions.Item label="Trace">{result.trace_id || '无'}</Descriptions.Item>
      {result.trace_evidence && (
        <>
          <Descriptions.Item label="工具调用">{result.trace_evidence.tool_call_count ?? 0}</Descriptions.Item>
          <Descriptions.Item label="工具错误">{result.trace_evidence.tool_error_count ?? 0}</Descriptions.Item>
          <Descriptions.Item label="Trace 延迟 (ms)">{result.trace_evidence.latency_ms ?? 0}</Descriptions.Item>
          <Descriptions.Item label="Trace 成本 (USD)">{(result.trace_evidence.cost_usd ?? 0).toFixed(4)}</Descriptions.Item>
          <Descriptions.Item label="Trace 成功">{result.trace_evidence.success ? '是' : '否'}</Descriptions.Item>
        </>
      )}
      <Descriptions.Item label="实际输出">{result.actual == null ? '无' : (typeof result.actual === 'string' ? result.actual : JSON.stringify(result.actual))}</Descriptions.Item>
      <Descriptions.Item label="失败归因">{result.failure_reason}</Descriptions.Item>
    </Descriptions>
  </div>
);

const dimensionScoreColumns: ColumnsType<DimensionScore> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '得分', dataIndex: 'score', key: 'score', render: (v: number) => v.toFixed(3) },
  { title: '通过', dataIndex: 'passed', key: 'passed', render: (v: boolean) => (v ? '是' : '否') },
  { title: '置信度', dataIndex: 'confidence', key: 'confidence', render: (v: number) => (v == null ? '-' : v.toFixed(2)) },
];

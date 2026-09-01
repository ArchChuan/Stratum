import { Descriptions, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMemo, useState } from 'react';

import type { DimensionScore, EvaluationRun, ToolObservation } from '../model/evaluation';

type ClusterRow = { key: string; reason: string; count: number; failedCaseIds: string[] };

const toolSequenceColumns: ColumnsType<ToolObservation> = [
  { title: '步骤', dataIndex: 'step_index', width: 56 },
  { title: '工具', dataIndex: 'tool_name', width: 120 },
  { title: '类型', dataIndex: 'tool_type', width: 72 },
  { title: '提供方', dataIndex: 'provider_type', width: 96 },
  { title: '能力', dataIndex: 'capability_id', width: 120, ellipsis: true },
  { title: '参数', dataIndex: 'arguments', render: (v: unknown) => (v == null ? '-' : JSON.stringify(v)) },
  { title: '原文', dataIndex: 'raw_text', ellipsis: true },
];

// ToolSequenceTable 渲染一次执行链路中的工具调用序列（§6.5），供过程断言
// 归因与评审详情展示。
const ToolSequenceTable = ({ tools }: { tools: ToolObservation[] }) => (
  <Table<ToolObservation> rowKey={(_, index) => String(index ?? 0)} size="small" pagination={false}
    dataSource={tools} columns={toolSequenceColumns} />
);

// RunAttributionPanel implements spec §6.3 case-clustering attribution: failed
// cases grouped by failure_reason, with per-cluster count and a single-case
// drill-down showing dimension scores, trace id and actual output.
export const RunAttributionPanel = ({ results }: { results: EvaluationRun['results'] }) => {
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const clusters = useMemo<ClusterRow[]>(() => {
    const map = new Map<string, { count: number; ids: string[] }>();
    for (const r of results) {
      // 失败聚类同时纳入过程失败 case（passed=false 且无输出 failure_reason，但
      // process_pass===false）：过程归因单独在 ProcessFailure（spec §6.5），否则
      // 工具序列 drill-down 永远不可达。
      if (r.passed || (!r.failure_reason && r.process_pass !== false)) {
        continue;
      }
      const reason = r.failure_reason
        || (r.process_pass === false ? (r.process_failure || 'process:failed') : '');
      const entry = map.get(reason) ?? { count: 0, ids: [] };
      entry.count += 1;
      entry.ids.push(r.case_id);
      map.set(reason, entry);
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
    <Space size={8} wrap style={{ marginBottom: 8 }}>
      <Tag color={result.passed ? 'success' : 'error'}>{result.passed ? '输出通过' : '输出未通过'}</Tag>
      <Tag color={result.process_pass ? 'success' : 'error'}>{result.process_pass ? '过程通过' : '过程未通过'}</Tag>
    </Space>
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
      <Descriptions.Item label="失败归因">
        {result.failure_reason || (result.process_pass === false ? (result.process_failure || '-') : '-')}
      </Descriptions.Item>
      {result.process_pass === false && (
        <Descriptions.Item label="过程失败">{result.process_failure || '未知'}</Descriptions.Item>
      )}
    </Descriptions>
    {result.tools && result.tools.length > 0 && <>
      <Typography.Title level={5}>工具序列</Typography.Title>
      <ToolSequenceTable tools={result.tools} />
    </>}
  </div>
);

const dimensionScoreColumns: ColumnsType<DimensionScore> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '得分', dataIndex: 'score', key: 'score', render: (v: number) => v.toFixed(3) },
  { title: '通过', dataIndex: 'passed', key: 'passed', render: (v: boolean) => (v ? '是' : '否') },
  { title: '置信度', dataIndex: 'confidence', key: 'confidence', render: (v: number) => (v == null ? '-' : v.toFixed(2)) },
];

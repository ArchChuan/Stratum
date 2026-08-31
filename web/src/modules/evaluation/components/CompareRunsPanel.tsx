import { Descriptions, Empty, Select, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useState } from 'react';

import type { EvaluationRun, RunSummary } from '../model/evaluation';

type CompareRow = { name: string; baseScore: number; targetScore: number; delta: number; basePassRate: number; targetPassRate: number };

const columns: ColumnsType<CompareRow> = [
  { title: '维度', dataIndex: 'name', key: 'name' },
  { title: '基线平均分', dataIndex: 'baseScore', key: 'baseScore', render: (v: number) => v.toFixed(3) },
  { title: '对比平均分', dataIndex: 'targetScore', key: 'targetScore', render: (v: number) => v.toFixed(3) },
  { title: '差异', dataIndex: 'delta', key: 'delta', render: (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(3)}` },
  { title: '基线通过率', dataIndex: 'basePassRate', key: 'basePassRate', render: (v: number) => `${(v * 100).toFixed(1)}%` },
  { title: '对比通过率', dataIndex: 'targetPassRate', key: 'targetPassRate', render: (v: number) => `${(v * 100).toFixed(1)}%` },
];

type GetRun = (runId: string) => Promise<EvaluationRun>;

// CompareRunsPanel implements spec §6.3 attribution layer 1: same-suite /
// same-resource version comparison. The current run (target) is compared
// against a selected base run; per-dimension metric deltas surface which
// dimension regressed between the two versions.
export const CompareRunsPanel = ({ currentId, runs, getRun }: {
  currentId: string; runs: RunSummary[]; getRun: GetRun;
}) => {
  const [baseId, setBaseId] = useState<string | undefined>();
  const [base, setBase] = useState<EvaluationRun | null>(null);
  const [target, setTarget] = useState<EvaluationRun | null>(null);

  // Same-resource invariant: base-run candidates must come from the current
  // run's resource. center.runs.items is a cross-resource list, so without this
  // filter other resources' runs would surface as meaningless comparison baselines.
  const currentResourceId = runs.find((r) => r.id === currentId)?.resource_id;

  const candidates = useMemo(
    () => runs.filter((r) => r.id !== currentId && r.resource_id === currentResourceId),
    [runs, currentId, currentResourceId],
  );

  const load = useCallback(async (id: string) => {
    const detail = await getRun(id);
    if (id === currentId) {
      setTarget(detail);
    } else {
      setBase(detail);
    }
  }, [currentId, getRun]);

  useEffect(() => {
    void load(currentId);
  }, [currentId, load]);

  useEffect(() => {
    if (baseId) {
      void load(baseId);
    }
  }, [baseId, load]);

  if (candidates.length === 0) {
    return <Empty description="该资源只有一次运行，无法对比" />;
  }

  const baseDim = base?.metrics?.by_dimension as Record<string, { avg_score: number; pass_rate: number }> | undefined;
  const targetDim = target?.metrics?.by_dimension as Record<string, { avg_score: number; pass_rate: number }> | undefined;
  const names = new Set([...(baseDim ? Object.keys(baseDim) : []), ...(targetDim ? Object.keys(targetDim) : [])]);
  const rows: CompareRow[] = [...names].map((name) => {
    const baseScore = baseDim?.[name]?.avg_score ?? 0;
    const targetScore = targetDim?.[name]?.avg_score ?? 0;
    return {
      name,
      baseScore,
      targetScore,
      delta: targetScore - baseScore,
      basePassRate: baseDim?.[name]?.pass_rate ?? 0,
      targetPassRate: targetDim?.[name]?.pass_rate ?? 0,
    };
  });

  return (
    <div data-testid="compare-runs-panel">
      <Select
        aria-label="对比目标"
        style={{ width: 240, marginBottom: 16 }}
        placeholder="选择基线运行"
        value={baseId}
        onChange={setBaseId}
        options={candidates.map((r) => ({ value: r.id, label: `${r.revision_id}（${new Date(r.created_at).toLocaleDateString('zh-CN')}）` }))}
      />
      {base && target && <Table<CompareRow> rowKey="name" size="small" pagination={false} dataSource={rows} columns={columns} />}
      {base && <Descriptions bordered size="small" column={1} style={{ marginTop: 16 }}>
        <Descriptions.Item label="基线套件版本">{base.suite_revision_id}</Descriptions.Item>
        <Descriptions.Item label="对比套件版本">{target?.suite_revision_id}</Descriptions.Item>
      </Descriptions>}
    </div>
  );
};

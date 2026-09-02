// RuntimeHealthTrendPanel 运行态健康分趋势视图（spec §10.1）。
// 选择资源后，按时间展示其多次离线评测 run 的健康分（通过率 = passed_cases /
// total_cases）趋势。数据源为评测中心 runs 记录（多租户 DB），非 Prometheus；
// rule 命中率 / judge 均分 / 行为异常率的 Prom 时间序列属后续接入项（见 backlog）。
import { Alert, Empty, Flex, Select, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect, useMemo, useRef, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import type { ResourceKind, ResourceSummary, RunSummary } from '../model/evaluation';

import { HealthTrendChart } from './HealthTrendChart';
import type { HealthTrendPoint } from './HealthTrendChart';
import { StatusTag, displayLabel, runDisplayStatus } from './evaluationView';

import { extractErrorMessage } from '@/shared/lib';

const TREND_LIMIT = 100;
const kindOptions = ['skill', 'agent', 'mcp', 'knowledge'].map((value) => ({ value, label: displayLabel(value) }));

// shortTrendTime 生成 x 轴短标签（MM-DD HH:mm），完整时间用于 tooltip/表格。
function shortTrendTime(iso: string): string {
  const date = new Date(iso);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function passRateOf(run: RunSummary): number | null {
  if (!run.total_cases || run.total_cases <= 0) return null;
  return run.passed_cases / run.total_cases;
}

type TrendRow = RunSummary & { pass_rate: number | null };

export const RuntimeHealthTrendPanel = ({ defaultKind, defaultResourceId }: {
  defaultKind?: ResourceKind; defaultResourceId?: string;
}) => {
  const [kind, setKind] = useState<ResourceKind | undefined>(defaultKind);
  const [resourceId, setResourceId] = useState(defaultResourceId ?? '');
  const [resources, setResources] = useState<ResourceSummary[]>([]);
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [resourcesLoading, setResourcesLoading] = useState(true);
  // 初始即给出 defaultKind+defaultResourceId 时，runs 拉取在挂载 effect 中进行，
  // 初始为 true 避免「尚无运行记录」空态闪现。
  const [runsLoading, setRunsLoading] = useState(Boolean(defaultKind && defaultResourceId));
  const [error, setError] = useState('');
  const mountedRef = useRef(true);
  useEffect(() => () => { mountedRef.current = false; }, []);

  // kind 变化时刷新资源候选并清空已选资源（可能不属于新类型）。
  useEffect(() => {
    let cancelled = false;
    setResourcesLoading(true);
    setError('');
    evaluationApi.listResources(kind ? { resource_kind: kind, limit: TREND_LIMIT } : { limit: TREND_LIMIT })
      .then((page) => { if (!cancelled) setResources(page.items); })
      .catch((err) => { if (!cancelled) setError(extractErrorMessage(err) || '加载资源列表失败'); })
      .finally(() => { if (!cancelled) setResourcesLoading(false); });
    return () => { cancelled = true; };
  }, [kind]);

  // 资源变化时拉取其 run 历史（按创建时间倒序，展示时升序）。
  useEffect(() => {
    if (!kind || !resourceId) { setRuns([]); return; }
    let cancelled = false;
    setRunsLoading(true);
    setError('');
    evaluationApi.listRuns({ resource_kind: kind, resource_id: resourceId, limit: TREND_LIMIT })
      .then((page) => { if (!cancelled) setRuns(page.items); })
      .catch((err) => { if (!cancelled) setError(extractErrorMessage(err) || '加载运行记录失败'); })
      .finally(() => { if (!cancelled) setRunsLoading(false); });
    return () => { cancelled = true; };
  }, [kind, resourceId]);

  const rows: TrendRow[] = useMemo(() => [...runs]
    .sort((a, b) => a.created_at.localeCompare(b.created_at))
    .map((run) => ({ ...run, pass_rate: passRateOf(run) })), [runs]);
  const points: HealthTrendPoint[] = useMemo(() => rows.map((row) => ({
    id: row.id, timeLabel: shortTrendTime(row.created_at), fullLabel: row.created_at,
    passRate: row.pass_rate, passed: row.passed,
  })), [rows]);
  const scoredRuns = rows.filter((row) => row.pass_rate !== null);
  const avgPassRate = scoredRuns.length
    ? scoredRuns.reduce((acc, row) => acc + (row.pass_rate as number), 0) / scoredRuns.length : null;
  const latest = scoredRuns.length ? scoredRuns[scoredRuns.length - 1].pass_rate as number : null;

  const columns: ColumnsType<TrendRow> = [
    { title: '时间', dataIndex: 'created_at', width: 150, render: (value: string) => (
      new Date(value).toLocaleString('zh-CN')) },
    { title: '运行', dataIndex: 'id', ellipsis: true },
    { title: '资源版本', dataIndex: 'revision_id', ellipsis: true },
    { title: '状态', dataIndex: 'status', width: 110, render: (value: string, row) => (
      <StatusTag value={runDisplayStatus(value, row.passed)} />) },
    { title: '用例', key: 'cases', width: 90, render: (_, row) => `${row.passed_cases}/${row.total_cases}` },
    { title: '通过率', dataIndex: 'pass_rate', width: 90, render: (value: number | null) => (
      value === null ? '-' : `${(value * 100).toFixed(1)}%`) },
  ];

  const percent = (value: number | null) => (value === null ? '-' : `${(value * 100).toFixed(1)}%`);

  return (
    <div data-testid="runtime-health-trend-panel">
      <Space wrap style={{ marginBottom: 12 }}>
        <Select aria-label="资源类型" allowClear placeholder="资源类型" style={{ width: 132 }} options={kindOptions}
          value={kind} loading={resourcesLoading}
          onChange={(value: ResourceKind | undefined) => { setKind(value); setResourceId(''); }} />
        <Select aria-label="资源" placeholder="选择资源以查看运行健康趋势" style={{ width: 260 }} options={resources.map((item) => ({
          value: item.resource_id, label: item.resource_id }))}
          value={resourceId || undefined} loading={resourcesLoading}
          onChange={(value: string) => setResourceId(value)} />
      </Space>
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} />}
      {!resourceId
        ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="选择资源类型与资源后展示多次运行的健康分趋势" />
        : rows.length === 0
          ? (runsLoading ? null : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该资源尚无评测运行记录" />)
          : <>
            <Flex gap={24} wrap style={{ marginBottom: 8 }}>
              <Typography.Text strong>健康分趋势（通过率）</Typography.Text>
              <Typography.Text type="secondary">运行 {rows.length} 次</Typography.Text>
              <Typography.Text type="secondary">平均健康分 {percent(avgPassRate)}</Typography.Text>
              <Typography.Text type="secondary">最近一次 {percent(latest)}</Typography.Text>
            </Flex>
            <HealthTrendChart points={points} />
            <Typography.Title level={5} style={{ marginTop: 12 }}>运行明细</Typography.Title>
            <Table<TrendRow> size="small" rowKey="id" dataSource={rows} columns={columns} pagination={false}
              loading={runsLoading} locale={{ emptyText: '暂无运行记录' }} />
          </>}
    </div>
  );
};

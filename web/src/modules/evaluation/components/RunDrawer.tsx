import { Alert, Descriptions, Drawer, Progress, Skeleton, Tabs, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import type { RunSummary } from '../model/evaluation';

import { drawerWidth, runDisplayStatus, StatusTag } from './evaluationView';

// metricLabels maps aggregated run metrics (eval_runs.metrics keys produced by
// the backend) to their user-visible Chinese labels; unknown keys fall back to
// the raw key.
const metricLabels: Record<string, string> = {
  pass_rate: '通过率',
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

function formatMetric(key: string, value: unknown): string {
  if (typeof value !== 'number') {
    return String(value ?? '');
  }
  if (key === 'pass_rate') {
    return `${(value * 100).toFixed(1)}%`;
  }
  if (Number.isInteger(value)) {
    return String(value);
  }
  return value.toFixed(4);
}

export const RunDrawer = ({ run, open, onClose, isMobile }: {
  run: RunSummary | null; open: boolean; onClose: () => void; isMobile?: boolean;
}) => {
  const [metrics, setMetrics] = useState<Record<string, unknown> | null>(null);

  useEffect(() => {
    if (!open || !run) {
      return;
    }
    let cancelled = false;
    setMetrics(null);
    void evaluationApi.getRun(run.id)
      .then((detail) => {
        if (!cancelled) {
          setMetrics(detail.metrics ?? {});
        }
      })
      .catch(() => {
        if (!cancelled) {
          setMetrics({});
        }
      });
    return () => {
      cancelled = true;
    };
  }, [open, run]);

  return (
    <Drawer title="评测运行" open={open} onClose={onClose} width={drawerWidth(isMobile)} destroyOnHidden>
      {run && <Tabs
        items={[
          {
            key: 'facts',
            label: '观测事实',
            children: <>
              <Typography.Title level={5}>观测事实</Typography.Title>
              <Descriptions bordered size="small" column={isMobile ? 1 : 2}>
                <Descriptions.Item label="运行状态"><StatusTag value={runDisplayStatus(run.status, run.passed)} /></Descriptions.Item>
                <Descriptions.Item label="资源版本">{run.revision_id}</Descriptions.Item>
                <Descriptions.Item label="通过用例">{run.passed_cases} / {run.total_cases}</Descriptions.Item>
                <Descriptions.Item label="创建时间">{new Date(run.created_at).toLocaleString('zh-CN')}</Descriptions.Item>
              </Descriptions>
              <Progress percent={run.total_cases ? Math.round(run.passed_cases / run.total_cases * 100) : 0} />
              {!run.passed && <Alert type="warning" showIcon message="运行未通过，请依据已脱敏的失败摘要定位问题。" />}
            </>,
          },
          {
            key: 'metrics',
            label: '指标',
            children: metrics === null
              ? <Skeleton active paragraph={{ rows: 4 }} />
              : <Descriptions bordered size="small" column={1}>
                {Object.entries(metrics).map(([key, value]) => (
                  <Descriptions.Item key={key} label={metricLabels[key] ?? key}>
                    {formatMetric(key, value)}
                  </Descriptions.Item>
                ))}
              </Descriptions>,
          },
        ]}
      />}
    </Drawer>
  );
};

import { ReloadOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Button, Card, Modal, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';

import { mechanismApi } from '../api/mechanism.api';
import type { MatrixCell, MatrixReport } from '../model/mechanism';

import { extractErrorMessage } from '@/shared/lib';

const STATUS_COLORS: Record<string, string> = { active: 'green', draft: 'orange' };
const STATUS_LABELS: Record<string, string> = { active: '生效', draft: '建档' };

/** 格式化指标：pass_rate 百分比 / 成本美元 / 延迟毫秒。 */
function formatMetric(cell: MatrixCell, kind: 'rate' | 'cost' | 'latency'): string {
  if (!cell.run_id) {
    return '-';
  }
  switch (kind) {
    case 'rate':
      return `${(cell.pass_rate * 100).toFixed(1)}%`;
    case 'cost':
      return `$${cell.total_cost.toFixed(4)}`;
    default:
      return `${Math.round(cell.avg_latency)}ms`;
  }
}

/** 矩阵工作台：基准集 × 档案矩阵评测报告 + 触发/采纳动作。 */
export const MatrixWorkbench = () => {
  const [report, setReport] = useState<MatrixReport>({ suites: [], cells: [], frontier_keys: [] });
  const [loading, setLoading] = useState(true);
  const [runLoading, setRunLoading] = useState(false);
  const [adopting, setAdopting] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      setReport(await mechanismApi.matrixReport());
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加载评测矩阵失败'), duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const runMatrix = useCallback(() => {
    Modal.confirm({
      title: '触发矩阵评测',
      content: '将为全部档案 × 当前基准集排队评测（异步执行）。档案指纹变化会以新幂等键触发重评测。',
      okText: '触发评测',
      cancelText: '取消',
      onOk: async () => {
        setRunLoading(true);
        try {
          const result = await mechanismApi.runMatrix();
          message.success({ content: `已排队 ${result.triggered_count} 个档案评测`, duration: 2 });
          void reload();
        } catch (err) {
          message.error({ content: extractErrorMessage(err, '触发评测失败'), duration: 0 });
        } finally {
          setRunLoading(false);
        }
      },
    });
  }, [reload]);

  const adopt = useCallback((cell: MatrixCell) => {
    Modal.confirm({
      title: `采纳档案「${cell.display_name || cell.family_key}」`,
      content: '采纳后该档案由建档（draft）转为生效（active），消费路径立即取用其基线。',
      okText: '采纳',
      cancelText: '取消',
      onOk: async () => {
        setAdopting(cell.family_key);
        try {
          await mechanismApi.adopt(cell.family_key);
          message.success({ content: '已采纳，档案生效', duration: 2 });
          void reload();
        } catch (err) {
          message.error({ content: extractErrorMessage(err, '采纳失败'), duration: 0 });
        } finally {
          setAdopting(null);
        }
      },
    });
  }, [reload]);

  const columns: ColumnsType<MatrixCell> = [
    {
      title: '族键',
      dataIndex: 'family_key',
      key: 'family_key',
      render: (v: string, row) => (
        <Typography.Text strong>
          {v} <Typography.Text type="secondary">v{row.version}</Typography.Text>
        </Typography.Text>
      ),
    },
    { title: '档案名称', dataIndex: 'display_name', key: 'display_name', render: (v: string) => v || '-' },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 90,
      render: (v?: string) =>
        v ? <Tag color={STATUS_COLORS[v]}>{STATUS_LABELS[v] || v}</Tag> : <Tag>未生效</Tag>,
    },
    { title: '富化模型', dataIndex: 'enrich_model', key: 'enrich_model', render: (v: string) => v || '-' },
    {
      title: '评测通过率',
      key: 'pass_rate',
      width: 110,
      render: (_, row) => (row.run_id ? formatMetric(row, 'rate') : <Tag color="default">未评测</Tag>),
    },
    { title: '单轮成本', key: 'total_cost', width: 100, render: (_, row) => formatMetric(row, 'cost') },
    { title: '平均延迟', key: 'avg_latency', width: 100, render: (_, row) => formatMetric(row, 'latency') },
    { title: '用例数', key: 'total_cases', width: 80, render: (_, row) => (row.run_id ? row.total_cases : '-') },
    {
      title: '前沿标注',
      key: 'frontier',
      width: 110,
      render: (_, row) =>
        row.frontier ? <Tag color="gold">帕累托前沿</Tag> : row.run_id ? <Tag>受支配</Tag> : '-',
    },
    {
      title: '操作',
      key: 'actions',
      width: 100,
      render: (_, row) =>
        row.status === 'draft' ? (
          <Button
            type="link"
            size="small"
            loading={adopting === row.family_key}
            onClick={() => adopt(row)}
          >
            采纳
          </Button>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
  ];

  const suites = report.suites;

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      <Card size="small" style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <Typography.Text strong>基准集</Typography.Text>
            {suites.length === 0 ? (
              <div>
                <Typography.Text type="secondary">尚无基准集，触发评测时自动创建</Typography.Text>
              </div>
            ) : (
              suites.map((s) => (
                <div key={s.id}>
                  <Typography.Text>{s.name}</Typography.Text>
                  <Typography.Text type="secondary">
                    {' '}
                    · 已发布 {s.active_revision || '-'} · {s.case_count} 个用例
                  </Typography.Text>
                </div>
              ))
            )}
          </div>
          <Space>
            <Button icon={<ReloadOutlined />} onClick={() => void reload()} loading={loading}>
              刷新
            </Button>
            <Button type="primary" icon={<ThunderboltOutlined />} loading={runLoading} onClick={runMatrix}>
              触发评测
            </Button>
          </Space>
        </div>
      </Card>

      <Card size="small" style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}>
        <Table<MatrixCell>
          rowKey="family_key"
          columns={columns}
          dataSource={report.cells}
          loading={loading}
          pagination={false}
          locale={{ emptyText: '暂无档案，请先到「模型档案」页建档' }}
        />
      </Card>

      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        帕累托前沿 = fidelity（通过率）/ cost（成本）/ perf（延迟）三维非支配；模型升级 → 指纹变化 →
        重评测 → 采纳，闭环驱动。
      </Typography.Text>
    </Space>
  );
};

import {
  Button,
  Empty,
  Modal,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { CSSProperties } from 'react';

import { parametersApi } from '../api/parameters.api';
import type {
  PlatformConfigVersion,
  PlatformValues,
} from '../model/parameters';

import { extractErrorMessage, isForbidden } from '@/shared/lib';

const { Text } = Typography;

// 版本状态 → 标签展示。draft 是唯一可编辑态，published 生效可回滚，
// archived 由保留上限自动修剪。
const STATUS_TAG: Record<string, { color: string; label: string }> = {
  draft: { color: 'orange', label: '草稿' },
  published: { color: 'green', label: '已发布' },
  archived: { color: 'default', label: '已归档' },
};

// 操作者展示：backfill 的 system 归因在审计视图里读起来是"系统初始化"。
const ACTOR_LABELS: Record<string, string> = { system: '系统' };

const actorLabel = (actor: string): string => ACTOR_LABELS[actor] ?? actor;

// 值与投影展示：undefined = 删除；对象/数组打印 JSON（快照值是 JSONB，可嵌套）。
const formatValue = (v: unknown): string => {
  if (typeof v === 'string') return v;
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
};

interface KeyDiff {
  key: string;
  before: unknown;
  after: unknown;
}

// diffSnapshots 返回 base → target 逐 key 变更（值相等者省略），按 key 排序保证
// 展开行稳定。base 缺失即"新增"（before undefined），target 缺失即"删除"。
const diffSnapshots = (
  base: Record<string, unknown>,
  target: Record<string, unknown>,
): KeyDiff[] => {
  const keys = new Set<string>([...Object.keys(base), ...Object.keys(target)]);
  const out: KeyDiff[] = [];
  for (const key of keys) {
    const before = base[key];
    const after = target[key];
    if (JSON.stringify(before) === JSON.stringify(after)) continue;
    out.push({ key, before, after });
  }
  return out.sort((a, b) => a.key.localeCompare(b.key));
};

const VersionHistory = ({
  groupKey,
  labelMap,
  refreshTick,
  onEffectiveChange,
}: {
  groupKey: string;
  labelMap?: Record<string, string>;
  refreshTick?: number;
  onEffectiveChange?: (values: PlatformValues) => void;
}) => {
  const [versions, setVersions] = useState<PlatformConfigVersion[]>([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const rows = await parametersApi.versions(groupKey);
      setVersions(rows ?? []);
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '加载版本历史失败'), duration: 3 });
      }
    } finally {
      setLoading(false);
    }
  }, [groupKey]);

  useEffect(() => {
    void load();
  }, [load, refreshTick]);

  // 生效快照按 id 索引，供 base_version_id diff 链与"当前生效"判定使用。
  const byId = useMemo(() => {
    const m = new Map<number, PlatformConfigVersion>();
    for (const v of versions) m.set(v.id, v);
    return m;
  }, [versions]);

  // production 所指版本 = 当前生效（回滚无意义，禁按钮）。由服务端按
  // production label 推导 is_current：前端不跨组拼快照字符串比对——真实多
  // 分组下 PlatformValues 是平铺 map、快照是分组粒度，JSON 比对恒 false，
  // 会让 production 所指版本也露出「回滚」按钮。
  const isCurrent = useCallback(
    (v: PlatformConfigVersion): boolean => v.status === 'published' && v.is_current,
    [],
  );

  // 发布/回滚成功后通过 onEffectiveChange 回传生效快照，父级据此递增 refreshTick
  // 触发重拉——避免 act 内直接 load() 与 tick 驱动造成双重请求。
  const act = useCallback(
    async (fn: () => Promise<PlatformValues>, successMsg: string) => {
      try {
        const values = await fn();
        message.success({ content: successMsg, duration: 2 });
        onEffectiveChange?.(values);
      } catch (err) {
        if (!isForbidden(err)) {
          message.error({ content: extractErrorMessage(err, '操作失败'), duration: 3 });
        }
      }
    },
    [onEffectiveChange],
  );

  const columns = useMemo<ColumnsType<PlatformConfigVersion>>(() => {
    const currentLabel = (v: PlatformConfigVersion) =>
      isCurrent(v) ? (
        <Tag color="blue" style={{ marginInlineEnd: 4 }}>
          当前生效
        </Tag>
      ) : null;

    return [
      {
        title: '版本',
        dataIndex: 'version_seq',
        width: 80,
        render: (seq: number) => `v${seq}`,
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 110,
        render: (_: unknown, v: PlatformConfigVersion) => {
          const tag = STATUS_TAG[v.status];
          return (
            <>
              {currentLabel(v)}
              <Tag color={tag?.color}>{tag?.label ?? v.status}</Tag>
            </>
          );
        },
      },
      {
        title: '变更说明',
        dataIndex: 'message',
        ellipsis: true,
        render: (msg: string) => msg || <Text type="secondary">—</Text>,
      },
      {
        title: '操作者',
        dataIndex: 'created_by',
        width: 120,
        render: (actor: string) => actorLabel(actor),
      },
      {
        title: '时间',
        dataIndex: 'created_at',
        width: 180,
        render: (t: string) => new Date(t).toLocaleString('zh-CN', { hour12: false }),
      },
      {
        title: '操作',
        key: 'actions',
        width: 120,
        render: (_: unknown, v: PlatformConfigVersion) => {
          if (v.status === 'draft') {
            return (
              <Button
                type="link"
                size="small"
                onClick={() => {
                  Modal.confirm({
                    title: `发布版本 v${v.version_seq}？`,
                    content: '发布后 production/latest 将指向该版本，参数立即生效。',
                    okText: '发布',
                    cancelText: '取消',
                    onOk: () =>
                      act(
                        () => parametersApi.publish(groupKey, v.id),
                        `版本 v${v.version_seq} 已发布`,
                      ),
                  });
                }}
              >
                发布
              </Button>
            );
          }
          if (v.status === 'published' && !isCurrent(v)) {
            return (
              <Button
                type="link"
                size="small"
                danger
                onClick={() => {
                  Modal.confirm({
                    title: `回滚到版本 v${v.version_seq}？`,
                    // 影响范围与既有平台的版本发布×租户资源变更责任边界一致：只影响
                    // 未显式声明该参数的租户资源（declared 优先），故明确写出来。
                    content:
                      '回滚后参数立即生效：所有未显式声明该参数的租户资源将回退到该版本的取值；' +
                      '不产生新版本，历史保留可再次回滚。',
                    okText: '回滚',
                    okButtonProps: { danger: true },
                    cancelText: '取消',
                    onOk: () =>
                      act(
                        () => parametersApi.rollback(groupKey, v.id),
                        `已回滚到版本 v${v.version_seq}`,
                      ),
                  });
                }}
              >
                回滚
              </Button>
            );
          }
          return null;
        },
      },
    ];
  }, [groupKey, isCurrent, act]);

  return (
    <div style={{ marginTop: 24 }}>
      <Space direction="vertical" size={8} style={{ width: '100%' }}>
        <Typography.Text strong>版本历史（配置变更审计）</Typography.Text>
        <Text type="secondary" style={{ fontSize: 12 }}>
          每次保存产生一个已发布版本；展开行对比该版本与其发布时基线的逐项变更，
          回滚将 production 指回历史版本并在审计表留痕。
        </Text>
        <Table<PlatformConfigVersion>
          rowKey="id"
          size="small"
          loading={loading}
          columns={columns}
          dataSource={versions}
          pagination={{ pageSize: 5, showSizeChanger: false }}
          expandable={{
            expandedRowRender: (v: PlatformConfigVersion) => {
              const base = v.base_version_id != null ? byId.get(v.base_version_id) : undefined;
              const baseSnapshot = base?.snapshot ?? {};
              const diffs = diffSnapshots(baseSnapshot, v.snapshot);
              if (diffs.length === 0) {
                return <Text type="secondary">无参数变更（首次发布或与基线一致）</Text>;
              }
              return (
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>
                    <tr>
                      <th style={diffTh}>参数</th>
                      <th style={diffTh}>变更前</th>
                      <th style={diffTh}>变更后</th>
                    </tr>
                  </thead>
                  <tbody>
                    {diffs.map((d) => (
                      <tr key={d.key} style={{ borderTop: '1px solid #f0f0f0' }}>
                        <td style={diffTd}>{labelMap?.[d.key] ?? d.key}</td>
                        <td style={diffTd}>
                          {d.before === undefined ? <Tag color="green">新增</Tag> : <code>{formatValue(d.before)}</code>}
                        </td>
                        <td style={diffTd}>
                          {d.after === undefined ? <Tag color="red">删除</Tag> : <code>{formatValue(d.after)}</code>}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              );
            },
          }}
          locale={{
            emptyText: loading ? null : <Empty description="暂无版本记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />,
          }}
        />
      </Space>
    </div>
  );
};

const diffTh: CSSProperties = {
  textAlign: 'left',
  padding: '4px 8px',
  fontWeight: 600,
  background: '#fafafa',
};
const diffTd: CSSProperties = {
  textAlign: 'left',
  padding: '4px 8px',
  verticalAlign: 'top',
  // 快照值是 JSONB，长 JSON（提示词、embedding 配置）会撑破表格，必须折行。
  wordBreak: 'break-word',
  overflowWrap: 'anywhere',
};

export default VersionHistory;

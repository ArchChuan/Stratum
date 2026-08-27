import { Button, Pagination, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect } from 'react';

import { useSummariesTab } from '../hooks/useSummariesTab';
import type { MemorySummary } from '../model/memory';

import { DangerPopconfirm, EmptyHint } from '@/shared/ui';

const columns = (onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemorySummary> => [
  { title: '摘要', dataIndex: 'summary', ellipsis: true },
  { title: '层级', dataIndex: 'tier', width: 130, render: (v: string) => <Tag>{v}</Tag> },
  { title: '重要度', dataIndex: 'importance', width: 90, render: (v: number) => v.toFixed(2) },
  { title: '覆盖至', dataIndex: 'period_end', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  { title: '创建时间', dataIndex: 'created_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  {
    title: '操作',
    key: 'action',
    width: 100,
    render: (_: unknown, record: MemorySummary) => (
      <DangerPopconfirm
        title="删除摘要"
        description="删除后该历史摘要不再进入记忆上下文，且无法恢复"
        onConfirm={() => onDelete(record.id)}
      >
        <Button type="link" size="small" danger loading={deleteLoading}>
          删除
        </Button>
      </DangerPopconfirm>
    ),
  },
];

export const SummaryTable = ({ onChanged, reloadKey }: { onChanged?: () => void; reloadKey?: number }) => {
  const { summaries, loading, deleteLoading, deleteSummary, pagination, reload } = useSummariesTab();

  useEffect(() => {
    if (reloadKey && reloadKey > 0) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅响应清空后的 reloadKey 变化；reload 内部已随翻页状态由 hook 自身 effect 触发
  }, [reloadKey]);

  const handleDelete = async (id: string) => {
    await deleteSummary(id);
    onChanged?.();
  };

  return (
    <div>
      <Table<MemorySummary>
        rowKey="id"
        columns={columns((id) => void handleDelete(id), deleteLoading)}
        dataSource={summaries}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <EmptyHint title="历史摘要还是空的" /> }}
      />
      <Pagination {...pagination} />
    </div>
  );
};

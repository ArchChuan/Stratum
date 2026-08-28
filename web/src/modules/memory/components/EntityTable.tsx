import { Button, Pagination, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect } from 'react';

import { useEntitiesTab } from '../hooks/useEntitiesTab';
import type { MemoryEntity } from '../model/memory';

import { MemoryScopeTag } from './MemoryScopeTag';

import { DangerPopconfirm, EmptyHint } from '@/shared/ui';

const columns = (onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemoryEntity> => [
  { title: '名称', dataIndex: 'name' },
  { title: '归属', dataIndex: 'scope', width: 100, render: (v: string) => <MemoryScopeTag scope={v} /> },
  { title: '类型', dataIndex: 'entity_type', width: 120 },
  { title: '关联事实数', dataIndex: 'fact_count', width: 110 },
  { title: '最近出现', dataIndex: 'last_seen_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
  {
    title: '操作',
    key: 'action',
    width: 100,
    render: (_: unknown, record: MemoryEntity) => (
      <DangerPopconfirm
        title="删除实体"
        description="删除后该实体不再出现在记忆话题中，且无法恢复"
        onConfirm={() => onDelete(record.id)}
      >
        <Button type="link" size="small" danger loading={deleteLoading}>
          删除
        </Button>
      </DangerPopconfirm>
    ),
  },
];

export const EntityTable = ({ onChanged, reloadKey }: { onChanged?: () => void; reloadKey?: number }) => {
  const { entities, loading, deleteLoading, deleteEntity, pagination, reload } = useEntitiesTab();

  useEffect(() => {
    if (reloadKey && reloadKey > 0) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅响应清空后的 reloadKey 变化；reload 内部已随翻页状态由 hook 自身 effect 触发
  }, [reloadKey]);

  const handleDelete = async (id: string) => {
    await deleteEntity(id);
    onChanged?.();
  };

  return (
    <div>
      <Table<MemoryEntity>
        rowKey="id"
        columns={columns((id) => void handleDelete(id), deleteLoading)}
        dataSource={entities}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <EmptyHint title="实体记忆还是空的" /> }}
      />
      <Pagination {...pagination} />
    </div>
  );
};

import { SearchOutlined } from '@ant-design/icons';
import { Button, Input, Pagination, Space, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useEffect } from 'react';

import { useEntriesTab } from '../hooks/useEntriesTab';
import type { MemoryEntryItem } from '../model/memory';

import { MemoryScopeTag } from './MemoryScopeTag';

import { DangerPopconfirm, EmptyHint } from '@/shared/ui';

const columns = (onDelete: (id: string) => void, deleteLoading: boolean): ColumnsType<MemoryEntryItem> => [
  { title: '内容', dataIndex: 'content', ellipsis: true },
  { title: '归属', dataIndex: 'scope', width: 100, render: (v: string) => <MemoryScopeTag scope={v} /> },
  { title: '角色', dataIndex: 'role', width: 80 },
  { title: '类型', dataIndex: 'type', width: 100 },
  { title: '重要度', dataIndex: 'importance', width: 90, render: (v: number) => v.toFixed(2) },
  {
    title: '过期时间',
    dataIndex: 'expires_at',
    width: 170,
    render: (v: string | null) => (v ? new Date(v).toLocaleString() : '不过期'),
  },
  {
    title: '操作',
    key: 'action',
    width: 100,
    render: (_: unknown, record: MemoryEntryItem) => (
      <DangerPopconfirm
        title="删除条目"
        description="删除后该条原始消息不再进入召回上下文，且无法恢复"
        onConfirm={() => onDelete(record.id)}
      >
        <Button type="link" size="small" danger loading={deleteLoading}>
          删除
        </Button>
      </DangerPopconfirm>
    ),
  },
];

export const EntryTable = ({ onChanged, reloadKey }: { onChanged?: () => void; reloadKey?: number }) => {
  const { entries, loading, deleteLoading, query, setQuery, deleteEntry, pagination, reload } = useEntriesTab();

  useEffect(() => {
    if (reloadKey && reloadKey > 0) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅响应清空后的 reloadKey 变化；reload 内部已随搜索/翻页状态由 hook 自身 effect 触发
  }, [reloadKey]);

  const handleDelete = async (id: string) => {
    await deleteEntry(id);
    onChanged?.();
  };

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Input
          placeholder="搜索条目内容"
          prefix={<SearchOutlined />}
          allowClear
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ width: 220 }}
        />
        <Button onClick={() => setQuery('')}>重置</Button>
      </Space>
      <Table<MemoryEntryItem>
        rowKey="id"
        columns={columns((id) => void handleDelete(id), deleteLoading)}
        dataSource={entries}
        loading={loading}
        pagination={false}
        locale={{ emptyText: <EmptyHint title={entries.length === 0 ? '原始条目还是空的' : '没有找到匹配的条目'} /> }}
      />
      <Pagination {...pagination} />
    </div>
  );
};

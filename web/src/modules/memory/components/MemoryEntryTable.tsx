import { DeleteOutlined } from '@ant-design/icons';
import { Button, Table, Tag, Tooltip, Typography } from 'antd';

import type { MemorySearchResult } from '../model/memory';

import { COMPACT_PAGE_SIZE } from '@/constants';
import { DangerPopconfirm } from '@/shared/ui';

const { Text } = Typography;

const importanceColor = (v?: number) => {
  if (v === undefined) return '#52c41a';
  if (v > 0.7) return '#f5222d';
  if (v > 0.5) return '#fa8c16';
  return '#52c41a';
};

interface MemoryRow {
  key: string | number;
  id?: string;
  type: string;
  content: string;
  tags: string[];
  importance?: number;
  timestamp?: string;
  score?: number;
}

const buildColumns = (onDelete: (id: string) => void) => [
  {
    title: '类型',
    dataIndex: 'type',
    key: 'type',
    width: 80,
    render: (type: string) => (
      <Tag color={type === 'user' ? 'blue' : type === 'assistant' ? 'green' : 'default'}>
        {type === 'user' ? '用户' : type === 'assistant' ? '助手' : '系统'}
      </Tag>
    ),
  },
  {
    title: '内容',
    dataIndex: 'content',
    key: 'content',
    ellipsis: true,
    render: (content: string) => (
      <Tooltip title={content} placement="topLeft">
        <Text ellipsis>{content}</Text>
      </Tooltip>
    ),
  },
  {
    title: '标签',
    dataIndex: 'tags',
    key: 'tags',
    width: 160,
    render: (tags: string[] | undefined) =>
      tags?.map((tag) => (
        <Tag key={tag} style={{ marginBottom: 2 }}>
          {tag}
        </Tag>
      )),
  },
  {
    title: '重要性',
    dataIndex: 'importance',
    key: 'importance',
    width: 80,
    align: 'right' as const,
    sorter: (a: MemoryRow, b: MemoryRow) => (a.importance || 0) - (b.importance || 0),
    render: (v?: number) => (
      <Text style={{ color: importanceColor(v), fontWeight: 600 }}>{v?.toFixed(2) ?? '-'}</Text>
    ),
  },
  {
    title: '时间',
    dataIndex: 'timestamp',
    key: 'timestamp',
    width: 150,
    render: (ts?: string) => (
      <Text type="secondary">{ts ? new Date(ts).toLocaleString('zh-CN') : '-'}</Text>
    ),
  },
  {
    title: '操作',
    key: 'action',
    width: 80,
    render: (_: unknown, record: MemoryRow) =>
      record.id ? (
        <DangerPopconfirm
          title="确定删除这条记忆？"
          okText="删除"
          onConfirm={() => onDelete(record.id!)}
        >
          <Button danger size="small" icon={<DeleteOutlined />} type="text" />
        </DangerPopconfirm>
      ) : null,
  },
];

interface MemoryEntryTableProps {
  results: MemorySearchResult[];
  onDelete: (id: string) => void;
}

export const MemoryEntryTable = ({ results, onDelete }: MemoryEntryTableProps) => {
  const data: MemoryRow[] = results.map((r, i) => ({
    key: r.entry?.id || i,
    id: r.entry?.id,
    type: r.entry?.role || 'system',
    content: r.entry?.content || '',
    tags: r.entry?.tags || [],
    importance: r.entry?.importance,
    timestamp: r.entry?.timestamp,
    score: r.score,
  }));

  return (
    <Table
      dataSource={data}
      columns={buildColumns(onDelete)}
      pagination={{ pageSize: COMPACT_PAGE_SIZE }}
      size="small"
    />
  );
};

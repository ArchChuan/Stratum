import { Table, Tag } from 'antd';

interface TopEntity {
  name: string;
  type: string;
  mention_count: number;
}

interface TopEntitiesTableProps {
  entities: TopEntity[];
  loading: boolean;
}

export const TopEntitiesTable = ({ entities, loading }: TopEntitiesTableProps) => {
  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (type: string) => <Tag color="blue">{type}</Tag>,
    },
    {
      title: '提及次数',
      dataIndex: 'mention_count',
      key: 'mention_count',
      sorter: (a: TopEntity, b: TopEntity) => a.mention_count - b.mention_count,
    },
  ];

  return (
    <Table
      columns={columns}
      dataSource={entities}
      rowKey="name"
      loading={loading}
      pagination={false}
      size="small"
    />
  );
};

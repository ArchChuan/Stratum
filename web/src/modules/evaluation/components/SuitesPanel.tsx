import { PlusOutlined } from '@ant-design/icons';
import { Button, Empty, Space, Table } from 'antd';

import type { SuiteSummary } from '../model/evaluation';

import { StatusTag } from './evaluationView';

export const SuitesPanel = ({ suites, loading, canManage, onOpen, onCreate, onDelete, canDelete }: {
  suites: SuiteSummary[]; loading: boolean; canManage: boolean;
  onOpen: (suite: SuiteSummary) => void; onCreate: () => void;
  onDelete: (suite: SuiteSummary) => void; canDelete: (suite: SuiteSummary) => boolean;
}) => (
  <Space direction="vertical" style={{ width: '100%' }}>
    {canManage && <Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>新建套件</Button>}
    <Table<SuiteSummary> size="small" rowKey="id" dataSource={suites} loading={loading} pagination={false}
      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={canManage ? '套件还是空的' : '套件还是空的（仅管理员可管理）'} /> }}
      columns={[
        { title: '名称', dataIndex: 'name', ellipsis: true },
        { title: '说明', dataIndex: 'description', ellipsis: true },
        { title: '状态', dataIndex: 'status', width: 120, render: (value: string) => <StatusTag value={value} /> },
        { title: '创建时间', dataIndex: 'created_at', width: 180 },
        {
          title: '操作', width: 150, render: (_: unknown, row: SuiteSummary) => (
            <>
              {canManage && (row.status === 'draft'
                ? <Button type="link" size="small" onClick={() => onOpen(row)}>管理</Button>
                : <Button type="link" size="small" disabled>已发布</Button>)}
              {canDelete(row) && <Button type="link" size="small" danger onClick={() => onDelete(row)}>删除</Button>}
            </>
          ),
        },
      ]} />
  </Space>
);

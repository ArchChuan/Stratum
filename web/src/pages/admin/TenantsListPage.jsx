import React, { useEffect, useState } from 'react';
import { Table, Button, Tag, Typography, Popconfirm, message } from 'antd';
import { getAllTenants, setTenantEnabled } from '../../services/api';

const { Title } = Typography;

const TenantsListPage = () => {
  const [tenants, setTenants] = useState([]);
  const [loading, setLoading] = useState(false);

  const fetchTenants = async () => {
    setLoading(true);
    try {
      const res = await getAllTenants();
      setTenants(res.data.tenants || []);
    } catch {
      message.error('获取租户列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchTenants(); }, []);

  const handleToggle = async (tenantId, currentStatus) => {
    const enabling = currentStatus !== 'active';
    try {
      await setTenantEnabled(tenantId, enabling);
      message.success(enabling ? '已启用' : '已禁用');
      fetchTenants();
    } catch (err) {
      message.error(err.response?.data?.message || '操作失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 80 },
    { title: '租户名称', dataIndex: 'name' },
    { title: 'Slug', dataIndex: 'slug' },
    { title: '成员数', dataIndex: 'member_count', render: (v) => v ?? '-' },
    {
      title: '状态', dataIndex: 'status',
      render: (status) => <Tag color={status === 'active' ? 'green' : 'red'}>{status === 'active' ? '启用' : '禁用'}</Tag>,
    },
    {
      title: '操作', key: 'action',
      render: (_, record) => (
        <Popconfirm
          title={`确认${record.status === 'active' ? '禁用' : '启用'}该租户？`}
          onConfirm={() => handleToggle(record.id, record.status)}
          okText="确认" cancelText="取消"
        >
          <Button size="small" danger={record.status === 'active'}>{record.status === 'active' ? '禁用' : '启用'}</Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <div>
      <Title level={4} style={{ marginBottom: 16 }}>所有租户</Title>
      <Table dataSource={tenants} columns={columns} rowKey="id" loading={loading} pagination={{ pageSize: 20 }} />
    </div>
  );
};

export default TenantsListPage;

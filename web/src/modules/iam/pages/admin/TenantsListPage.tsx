import { Table, Button, Tag, Typography, message, Card } from 'antd';
import { useEffect, useState } from 'react';

import { tenantApi } from '../../api/tenant.api';
import { useAuth } from '../../components/AuthContext';
import type { AdminTenant } from '../../model/auth';

import { DEFAULT_PAGE_SIZE } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';
import { DangerPopconfirm } from '@/shared/ui';

const { Title, Text } = Typography;

export const TenantsListPage = () => {
  // useAuth not strictly needed but kept for parity if future role gating required
  useAuth();
  const [tenants, setTenants] = useState<AdminTenant[]>([]);
  const [loading, setLoading] = useState(false);

  const fetchTenants = async () => {
    setLoading(true);
    try {
      setTenants(await tenantApi.listAllTenants());
    } catch {
      message.error('获取租户列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTenants();
  }, []);

  const handleToggle = async (tenantId: string, currentStatus?: string) => {
    const enabling = currentStatus !== 'active';
    try {
      await tenantApi.setTenantEnabled(tenantId, enabling);
      message.success(enabling ? '已启用' : '已禁用');
      fetchTenants();
    } catch (err) {
      message.error(extractErrorMessage(err, '操作失败'));
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 80 },
    {
      title: '租户名称',
      dataIndex: 'name',
      render: (name: string) => <Text strong>{name}</Text>,
    },
    {
      title: 'Slug',
      dataIndex: 'slug',
      render: (v: string) => (
        <Text type="secondary" style={{ fontFamily: 'monospace' }}>
          {v}
        </Text>
      ),
    },
    {
      title: '成员数',
      dataIndex: 'member_count',
      render: (v?: number) => (v ?? '-'),
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (status?: string) => (
        <Tag color={status === 'active' ? 'green' : 'red'} style={{ borderRadius: 6 }}>
          {status === 'active' ? '启用' : '禁用'}
        </Tag>
      ),
    },
    {
      title: '操作',
      key: 'action',
      render: (_: unknown, record: AdminTenant) => {
        const isActive = record.status === 'active';
        return (
          <DangerPopconfirm
            title={`确认${isActive ? '禁用' : '启用'}该租户？`}
            okText={isActive ? '禁用' : '启用'}
            onConfirm={() => handleToggle(String(record.id), record.status)}
          >
            <Button size="small" danger={isActive}>
              {isActive ? '禁用' : '启用'}
            </Button>
          </DangerPopconfirm>
        );
      },
    },
  ];

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <Title level={4} style={{ margin: 0 }}>
          所有租户
        </Title>
        <Text type="secondary" style={{ fontSize: 13 }}>
          查看和管理平台所有租户
        </Text>
      </div>
      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
        <Table
          dataSource={tenants}
          columns={columns}
          rowKey="id"
          loading={loading}
          pagination={{ pageSize: DEFAULT_PAGE_SIZE, showTotal: (t) => `共 ${t} 个租户` }}
          style={{ borderRadius: 12, overflow: 'hidden' }}
        />
      </Card>
    </div>
  );
};

export default TenantsListPage;

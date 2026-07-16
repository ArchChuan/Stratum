import { Button, Tag, Typography, message, Card, Flex, Space } from 'antd';
import { useEffect, useState } from 'react';

import { tenantApi } from '../../api/tenant.api';
import { useAuth } from '../../components/AuthContext';
import type { AdminTenant } from '../../model/auth';

import { DEFAULT_PAGE_SIZE } from '@/constants';
import { extractErrorMessage } from '@/shared/lib';
import { DangerPopconfirm, ResponsiveDataView } from '@/shared/ui';

const { Title, Text } = Typography;

export const TenantsListPage = () => {
  useAuth();
  const [tenants, setTenants] = useState<AdminTenant[]>([]);
  const [loading, setLoading] = useState(false);
  const [deleteLoadingId, setDeleteLoadingId] = useState<string | null>(null);

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

  const handleDelete = async (tenantId: string) => {
    setDeleteLoadingId(tenantId);
    try {
      await tenantApi.adminDeleteTenant(tenantId);
      message.success('租户已删除');
      fetchTenants();
    } catch (err) {
      message.error(extractErrorMessage(err, '删除失败'));
    } finally {
      setDeleteLoadingId(null);
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
        const id = String(record.id);
        return (
          <Space>
            <DangerPopconfirm
              title={`确认${isActive ? '禁用' : '启用'}该租户？`}
              okText={isActive ? '禁用' : '启用'}
              onConfirm={() => handleToggle(id, record.status)}
            >
              <Button size="small" danger={isActive}>
                {isActive ? '禁用' : '启用'}
              </Button>
            </DangerPopconfirm>
            <DangerPopconfirm
              title={`确认删除租户「${record.name}」？此操作不可恢复，所有数据将被永久清除。`}
              okText="确认删除"
              onConfirm={() => handleDelete(id)}
              disabled={record.is_default}
            >
              <Button
                size="small"
                danger
                loading={deleteLoadingId === id}
                disabled={record.is_default}
                title={record.is_default ? '默认租户不可删除' : undefined}
              >
                删除
              </Button>
            </DangerPopconfirm>
          </Space>
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
        <ResponsiveDataView
          rows={tenants}
          columns={columns}
          rowKey="id"
          loading={loading}
          pagination={{ pageSize: DEFAULT_PAGE_SIZE, showTotal: (t) => `共 ${t} 个租户` }}
          renderMobileItem={(tenant) => {
            const id = String(tenant.id);
            const isActive = tenant.status === 'active';
            return (
              <div style={{ padding: 12, borderBottom: '1px solid #f0f0f0' }}>
                <Flex justify="space-between" align="center" gap={8}>
                  <div style={{ minWidth: 0 }}>
                    <Text strong ellipsis style={{ display: 'block' }}>{tenant.name}</Text>
                    <Text type="secondary" copyable style={{ fontSize: 12 }}>{id}</Text>
                  </div>
                  <Tag color={isActive ? 'green' : 'red'}>{isActive ? '启用' : '禁用'}</Tag>
                </Flex>
                <Flex justify="space-between" align="center" gap={8} style={{ marginTop: 10 }}>
                  <Space size={12}>
                    <Text type="secondary">{tenant.member_count ?? '-'} 位成员</Text>
                    <Text type="secondary">
                      {tenant.created_at ? new Date(tenant.created_at).toLocaleDateString('zh-CN') : '-'}
                    </Text>
                  </Space>
                  <Space size={4}>
                    <DangerPopconfirm
                      title={`确认${isActive ? '禁用' : '启用'}该租户？`}
                      okText={isActive ? '禁用' : '启用'}
                      onConfirm={() => handleToggle(id, tenant.status)}
                    >
                      <Button size="small" danger={isActive}>{isActive ? '禁用' : '启用'}</Button>
                    </DangerPopconfirm>
                    <DangerPopconfirm
                      title={`确认删除租户「${tenant.name}」？此操作不可恢复，所有数据将被永久清除。`}
                      okText="确认删除"
                      onConfirm={() => handleDelete(id)}
                      disabled={tenant.is_default}
                    >
                      <Button
                        size="small"
                        danger
                        loading={deleteLoadingId === id}
                        disabled={tenant.is_default}
                        aria-label="删除租户"
                      >
                        删除
                      </Button>
                    </DangerPopconfirm>
                  </Space>
                </Flex>
              </div>
            );
          }}
        />
      </Card>
    </div>
  );
};

export default TenantsListPage;

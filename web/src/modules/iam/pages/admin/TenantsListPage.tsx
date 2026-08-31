import { PlusOutlined } from '@ant-design/icons';
import { Button, Tag, Typography, message, Card, Flex, Space } from 'antd';
import type { TablePaginationConfig } from 'antd/es/table';
import { useEffect, useState } from 'react';

import { tenantApi, type CreateAdminTenantInput } from '../../api/tenant.api';
import { useAuth } from '../../components/AuthContext';
import type { AdminTenant } from '../../model/auth';

import { CreateTenantModal } from './CreateTenantModal';

import { DEFAULT_PAGE_SIZE } from '@/constants';
import { usePlatformAdminCanEdit } from '@/modules/iam';
import { extractErrorMessage, isForbidden } from '@/shared/lib';
import { DangerPopconfirm, ResponsiveDataView } from '@/shared/ui';

const { Title, Text } = Typography;

export const TenantsListPage = () => {
  const { user } = useAuth();
  // 租户删除是全局管理动作，仅超级管理员可执行（后端 DELETE /admin/tenants/:id 用
  // RequireGlobalAdmin 守卫）；system_admin 可查看/创建/启停，但删除按钮禁用。
  const isGlobalAdmin = user?.global_role === 'global_admin';
  // 只读模式下写控件置灰（由路由级 PlatformAdminGate 提供）；后端中间件仍是强制点。
  const canEdit = usePlatformAdminCanEdit();
  const [tenants, setTenants] = useState<AdminTenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteLoadingId, setDeleteLoadingId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [pagination, setPagination] = useState({
    current: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    total: 0,
  });

  const fetchTenants = async (page: number, pageSize: number) => {
    setLoading(true);
    try {
      const result = await tenantApi.listAllTenants(page, pageSize);
      setTenants(result.tenants);
      setPagination({ current: result.page, pageSize: result.page_size, total: result.total });
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: '获取租户列表失败', duration: 3 });
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchTenants(1, DEFAULT_PAGE_SIZE);
  }, []);

  const handleToggle = async (tenantId: string, currentStatus?: string) => {
    const enabling = currentStatus !== 'active';
    try {
      await tenantApi.setTenantEnabled(tenantId, enabling);
      message.success({ content: enabling ? '已启用' : '已禁用', duration: 2 });
      void fetchTenants(pagination.current, pagination.pageSize);
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '操作失败'), duration: 3 });
      }
    }
  };

  const handleDelete = async (tenantId: string) => {
    setDeleteLoadingId(tenantId);
    try {
      await tenantApi.adminDeleteTenant(tenantId);
      message.success({ content: '租户已删除', duration: 2 });
      void fetchTenants(pagination.current, pagination.pageSize);
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '删除失败'), duration: 3 });
      }
    } finally {
      setDeleteLoadingId(null);
    }
  };

  const handleCreate = async (input: CreateAdminTenantInput) => {
    setCreateLoading(true);
    try {
      await tenantApi.createTenant(input);
      message.success({ content: '租户创建成功', duration: 2 });
      setCreateOpen(false);
      await fetchTenants(pagination.current, pagination.pageSize);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '创建租户失败'), duration: 3 });
      throw err;
    } finally {
      setCreateLoading(false);
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
              <Button size="small" danger={isActive} disabled={!canEdit}>
                {isActive ? '禁用' : '启用'}
              </Button>
            </DangerPopconfirm>
            <DangerPopconfirm
              title={`确认删除租户「${record.name}」？此操作不可恢复，所有数据将被永久清除。`}
              okText="确认删除"
              onConfirm={() => handleDelete(id)}
              disabled={record.is_default || !isGlobalAdmin}
            >
              <Button
                size="small"
                danger
                loading={deleteLoadingId === id}
                disabled={record.is_default || !isGlobalAdmin}
                title={
                  record.is_default
                    ? '默认租户不可删除'
                    : isGlobalAdmin
                      ? undefined
                      : '仅超级管理员可删除'
                }
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
      <Flex justify="space-between" align="center" gap={16} style={{ marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            所有租户
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            查看和管理平台所有租户
          </Text>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          aria-label="创建租户"
          disabled={!canEdit}
          onClick={() => setCreateOpen(true)}
        >
          创建租户
        </Button>
      </Flex>
      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
        <ResponsiveDataView
          rows={tenants}
          columns={columns}
          rowKey="id"
          loading={loading}
          pagination={{
            current: pagination.current,
            pageSize: pagination.pageSize,
            total: pagination.total,
            showTotal: (t) => `共 ${t} 个租户`,
          }}
          mobilePaginationMode="server"
          onChange={(next: TablePaginationConfig) => {
            void fetchTenants(next.current || 1, next.pageSize || DEFAULT_PAGE_SIZE);
          }}
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
                      <Button size="small" danger={isActive} disabled={!canEdit}>{isActive ? '禁用' : '启用'}</Button>
                    </DangerPopconfirm>
                    <DangerPopconfirm
                      title={`确认删除租户「${tenant.name}」？此操作不可恢复，所有数据将被永久清除。`}
                      okText="确认删除"
                      onConfirm={() => handleDelete(id)}
                      disabled={tenant.is_default || !isGlobalAdmin}
                    >
                      <Button
                        size="small"
                        danger
                        loading={deleteLoadingId === id}
                        disabled={tenant.is_default || !isGlobalAdmin}
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
      <CreateTenantModal
        open={createOpen}
        loading={createLoading}
        onCancel={() => setCreateOpen(false)}
        onCreate={handleCreate}
      />
    </div>
  );
};

export default TenantsListPage;

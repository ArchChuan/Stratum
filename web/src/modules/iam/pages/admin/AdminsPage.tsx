import { PlusOutlined } from '@ant-design/icons';
import { Avatar, Button, Card, Flex, Tag, Typography, message } from 'antd';
import { useEffect, useState } from 'react';

import { tenantApi } from '../../api/tenant.api';
import { AdminAddModal, type AdminCandidate } from './AdminAddModal';

import { extractErrorMessage, isForbidden } from '@/shared/lib';
import { DangerPopconfirm, ResponsiveDataView } from '@/shared/ui';

const { Title, Text } = Typography;

const ROLE_META: Record<string, { color: string; label: string }> = {
  system_admin: { color: 'blue', label: '平台管理员' },
  global_admin: { color: 'gold', label: '超级管理员' },
};

/** 平台管理员列表页：查看、添加、移除 system_admin；global_admin 仅展示不可操作。 */
export const AdminsPage = () => {
  const [admins, setAdmins] = useState<AdminCandidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [removeLoadingId, setRemoveLoadingId] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);

  const fetchAdmins = async () => {
    setLoading(true);
    try {
      const list = await tenantApi.listAdmins();
      setAdmins(list);
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: '获取平台管理员列表失败', duration: 3 });
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchAdmins();
  }, []);

  const handleRemove = async (adminID: string) => {
    setRemoveLoadingId(adminID);
    try {
      await tenantApi.removeAdminRole(adminID);
      message.success({ content: '已移除平台管理员', duration: 2 });
      void fetchAdmins();
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '移除失败'), duration: 3 });
      }
    } finally {
      setRemoveLoadingId(null);
    }
  };

  const handleAdd = async (candidate: AdminCandidate) => {
    try {
      await tenantApi.setAdminRole(candidate.user_id);
      message.success({ content: '已添加平台管理员', duration: 2 });
      setAddOpen(false);
      void fetchAdmins();
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '添加失败'), duration: 3 });
    }
  };

  const columns = [
    {
      title: '用户',
      key: 'user',
      render: (_: unknown, record: AdminCandidate) => (
        <Flex align="center" gap={8}>
          <Avatar src={record.avatar_url}>{record.username?.[0] ?? '?'}</Avatar>
          <div style={{ lineHeight: 1.4 }}>
            <Text strong>{record.username || record.github_login || record.user_id}</Text>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>
                {record.user_id}
              </Text>
            </div>
          </div>
        </Flex>
      ),
    },
    {
      title: '角色',
      dataIndex: 'global_role',
      render: (role: string) => {
        const meta = ROLE_META[role] ?? { color: 'default', label: role };
        return <Tag color={meta.color}>{meta.label}</Tag>;
      },
    },
    {
      title: '操作',
      key: 'action',
      render: (_: unknown, record: AdminCandidate) => {
        const isSuperAdmin = record.global_role === 'global_admin';
        const displayName = record.username || record.github_login || record.user_id;
        return (
          <DangerPopconfirm
            title={`确认移除「${displayName}」的平台管理员权限？`}
            okText="确认移除"
            onConfirm={() => handleRemove(record.user_id)}
            disabled={isSuperAdmin}
          >
            <Button
              size="small"
              danger
              disabled={isSuperAdmin}
              loading={removeLoadingId === record.user_id}
            >
              移除
            </Button>
          </DangerPopconfirm>
        );
      },
    },
  ];

  return (
    <div>
      <Flex justify="space-between" align="center" gap={16} style={{ marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            平台管理员
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            管理平台级管理员。超级管理员仅由系统预设，不可在此变更。
          </Text>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          aria-label="添加平台管理员"
          onClick={() => setAddOpen(true)}
        >
          添加管理员
        </Button>
      </Flex>
      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
        <ResponsiveDataView
          rows={admins}
          columns={columns}
          rowKey="user_id"
          loading={loading}
          pagination={false}
          renderMobileItem={(record: AdminCandidate) => {
            const isSuperAdmin = record.global_role === 'global_admin';
            const meta = ROLE_META[record.global_role] ?? { color: 'default', label: record.global_role };
            const displayName = record.username || record.github_login || record.user_id;
            return (
              <div style={{ padding: 12, borderBottom: '1px solid #f0f0f0' }}>
                <Flex justify="space-between" align="center" gap={8}>
                  <Flex align="center" gap={8} style={{ minWidth: 0 }}>
                    <Avatar src={record.avatar_url}>{record.username?.[0] ?? '?'}</Avatar>
                    <div style={{ minWidth: 0 }}>
                      <Text strong ellipsis style={{ display: 'block' }}>
                        {displayName}
                      </Text>
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {record.user_id}
                      </Text>
                    </div>
                  </Flex>
                  <Tag color={meta.color}>{meta.label}</Tag>
                </Flex>
                <Flex justify="flex-end" style={{ marginTop: 10 }}>
                  <DangerPopconfirm
                    title={`确认移除「${displayName}」的平台管理员权限？`}
                    okText="确认移除"
                    onConfirm={() => handleRemove(record.user_id)}
                    disabled={isSuperAdmin}
                  >
                    <Button
                      size="small"
                      danger
                      disabled={isSuperAdmin}
                      loading={removeLoadingId === record.user_id}
                    >
                      移除
                    </Button>
                  </DangerPopconfirm>
                </Flex>
              </div>
            );
          }}
        />
      </Card>
      <AdminAddModal open={addOpen} onCancel={() => setAddOpen(false)} onAdd={handleAdd} />
    </div>
  );
};

export default AdminsPage;

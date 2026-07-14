import { DeleteOutlined, DownOutlined, MoreOutlined } from '@ant-design/icons';
import { Avatar, Button, Card, Dropdown, Flex, Space, Tag, Typography } from 'antd';
import type { TablePaginationConfig } from 'antd/es/table';

import type { Member } from '../model/auth';

import { PAGE_SIZE_OPTIONS } from '@/constants';
import { DangerPopconfirm, ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;

const ROLE_TAG: Record<string, { color: string; label: string }> = {
  owner: { color: 'gold', label: '超级管理员' },
  admin: { color: 'blue', label: '管理员' },
  member: { color: 'default', label: '普通成员' },
};

const buildRoleMenuItems = (record: Member) => {
  const items: { key: string; label: string }[] = [];
  if (record.role !== 'admin') items.push({ key: 'admin', label: '设为管理员' });
  if (record.role !== 'member') items.push({ key: 'member', label: '设为普通成员' });
  return items;
};

interface Props {
  members: Member[];
  loading: boolean;
  currentUserSub?: string;
  currentUserRole?: string;
  isOwner: boolean;
  pagination: { current: number; pageSize: number; total: number };
  onRemove: (userId: string) => void;
  onRoleChange: (userId: string, role: string) => void;
  onChange: (pagination: TablePaginationConfig) => void;
}

export const TenantMemberTable = ({
  members,
  loading,
  currentUserSub,
  currentUserRole,
  isOwner,
  pagination,
  onRemove,
  onRoleChange,
  onChange,
}: Props) => {
  const renderActions = (record: Member, compact = false) => {
    const isSelf = record.user_id === currentUserSub;
    if (isSelf) return <Tag style={{ borderRadius: 6 }}>（您）</Tag>;
    if (record.role === 'owner') return null;

    const canRemove = isOwner || (currentUserRole === 'admin' && record.role === 'member');
    const menuItems = isOwner ? buildRoleMenuItems(record) : [];

    return (
      <Space>
        {isOwner && menuItems.length > 0 && (
          <Dropdown
            menu={{
              items: menuItems,
              onClick: ({ key }) => onRoleChange(record.user_id, key),
            }}
            trigger={['click']}
          >
            <Button
              size="small"
              aria-label="变更角色"
              icon={compact ? <MoreOutlined /> : undefined}
            >
              {compact ? null : <>变更角色 <DownOutlined /></>}
            </Button>
          </Dropdown>
        )}
        {canRemove && (
          <DangerPopconfirm
            title="确认移除该成员？"
            okText="移除"
            onConfirm={() => onRemove(record.user_id)}
          >
            <Button
              danger
              size="small"
              aria-label="移除"
              icon={compact ? <DeleteOutlined /> : undefined}
            >
              {compact ? null : '移除'}
            </Button>
          </DangerPopconfirm>
        )}
      </Space>
    );
  };

  const columns = [
    {
      title: '用户',
      dataIndex: 'github_login',
      render: (login: string, record: Member) => (
        <Space>
          <Avatar src={record.avatar_url} size={32}>
            {login?.[0]?.toUpperCase()}
          </Avatar>
          <Text strong style={{ fontSize: 14 }}>
            {login}
          </Text>
        </Space>
      ),
    },
    {
      title: '角色',
      dataIndex: 'role',
      render: (role: string) => {
        const cfg = ROLE_TAG[role] || { color: 'default', label: role };
        return (
          <Tag color={cfg.color} style={{ borderRadius: 6 }}>
            {cfg.label}
          </Tag>
        );
      },
    },
    {
      title: '加入时间',
      dataIndex: 'joined_at',
      render: (v?: string) => (v ? new Date(v).toLocaleDateString('zh-CN') : '-'),
    },
    {
      title: '操作',
      key: 'action',
      render: (_: unknown, record: Member) => renderActions(record),
    },
  ];

  return (
    <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
      <ResponsiveDataView
        rows={members}
        columns={columns}
        rowKey="user_id"
        loading={loading}
        pagination={{
          current: pagination.current,
          pageSize: pagination.pageSize,
          total: pagination.total,
          showSizeChanger: true,
          pageSizeOptions: PAGE_SIZE_OPTIONS,
          showQuickJumper: true,
          showTotal: (t) => `共 ${t} 位成员`,
          style: { padding: '12px 16px' },
        }}
        mobilePaginationMode="server"
        onChange={onChange}
        renderMobileItem={(member) => {
          const role = ROLE_TAG[member.role] || { color: 'default', label: member.role };
          return (
            <div style={{ padding: 12, borderBottom: '1px solid #f0f0f0' }}>
              <Flex justify="space-between" align="center" gap={8}>
                <Space>
                  <Avatar src={member.avatar_url} size={32}>
                    {member.github_login?.[0]?.toUpperCase()}
                  </Avatar>
                  <Text strong>{member.github_login}</Text>
                </Space>
                <Tag color={role.color}>{role.label}</Tag>
              </Flex>
              <Flex justify="space-between" align="center" gap={8} style={{ marginTop: 10 }}>
                <Text type="secondary">
                  {member.joined_at ? new Date(member.joined_at).toLocaleDateString('zh-CN') : '-'}
                </Text>
                {renderActions(member, true)}
              </Flex>
            </div>
          );
        }}
      />
    </Card>
  );
};

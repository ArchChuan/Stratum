import React, { useEffect, useState } from 'react';
import { Table, Button, Modal, Form, Input, Select, Avatar, Tag, Space, Typography, Popconfirm, Dropdown, message } from 'antd';
import { UserAddOutlined, DownOutlined } from '@ant-design/icons';
import { getTenantMembers, inviteMember, removeMember, updateMemberRole } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Title } = Typography;

const ROLE_TAG = {
  owner: { color: 'gold', label: '超级管理员' },
  admin: { color: 'blue', label: '管理员' },
  member: { color: 'default', label: '普通成员' },
};

const MembersPage = () => {
  const { user } = useAuth();
  const [members, setMembers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteLoading, setInviteLoading] = useState(false);
  const [form] = Form.useForm();
  const isOwner = user?.role === 'owner';
  const canInvite = user?.role === 'owner' || user?.role === 'admin';

  const fetchMembers = async () => {
    setLoading(true);
    try {
      const res = await getTenantMembers();
      setMembers(res.data.members || []);
    } catch {
      message.error('获取成员列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchMembers(); }, []);

  const handleInvite = async (values) => {
    setInviteLoading(true);
    try {
      await inviteMember(values);
      message.success('邀请已发送');
      setInviteOpen(false);
      form.resetFields();
      fetchMembers();
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '邀请失败');
    } finally {
      setInviteLoading(false);
    }
  };

  const handleRemove = async (userId) => {
    try {
      await removeMember(userId);
      message.success('成员已移除');
      fetchMembers();
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '移除失败');
    }
  };

  const handleRoleChange = async (userId, role) => {
    try {
      await updateMemberRole(userId, role);
      message.success('角色已更新');
      fetchMembers();
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '更新角色失败');
    }
  };

  const buildRoleMenuItems = (record) => {
    const items = [];
    if (record.role !== 'admin') {
      items.push({ key: 'admin', label: '设为管理员' });
    }
    if (record.role !== 'member') {
      items.push({ key: 'member', label: '设为普通成员' });
    }
    return items;
  };

  const columns = [
    {
      title: '用户', dataIndex: 'github_login',
      render: (login, record) => (
        <Space>
          <Avatar src={record.avatar_url} size="small">{login?.[0]?.toUpperCase()}</Avatar>
          {login}
        </Space>
      ),
    },
    {
      title: '角色', dataIndex: 'role',
      render: (role) => {
        const cfg = ROLE_TAG[role] || { color: 'default', label: role };
        return <Tag color={cfg.color}>{cfg.label}</Tag>;
      },
    },
    {
      title: '加入时间', dataIndex: 'joined_at',
      render: (v) => v ? new Date(v).toLocaleDateString('zh-CN') : '-',
    },
    {
      title: '操作', key: 'action',
      render: (_, record) => {
        const isSelf = record.user_id === user?.sub;
        if (isSelf) return <Tag>（您）</Tag>;
        if (record.role === 'owner') return null;

        const canRemove = isOwner || (user?.role === 'admin' && record.role === 'member');
        const menuItems = isOwner ? buildRoleMenuItems(record) : [];

        return (
          <Space>
            {isOwner && menuItems.length > 0 && (
              <Dropdown
                menu={{
                  items: menuItems,
                  onClick: ({ key }) => handleRoleChange(record.user_id, key),
                }}
                trigger={['click']}
              >
                <Button size="small">
                  变更角色 <DownOutlined />
                </Button>
              </Dropdown>
            )}
            {canRemove && (
              <Popconfirm title="确认移除该成员？" onConfirm={() => handleRemove(record.user_id)} okText="确认" cancelText="取消">
                <Button danger size="small">移除</Button>
              </Popconfirm>
            )}
          </Space>
        );
      },
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>成员管理</Title>
        {canInvite && <Button type="primary" icon={<UserAddOutlined />} onClick={() => setInviteOpen(true)}>邀请成员</Button>}
      </div>
      <Table dataSource={members} columns={columns} rowKey="user_id" loading={loading} pagination={{ pageSize: 20 }} />
      <Modal title="邀请成员" open={inviteOpen} onCancel={() => { setInviteOpen(false); form.resetFields(); }} footer={null}>
        <Form form={form} layout="vertical" onFinish={handleInvite}>
          <Form.Item label="邮箱" name="email" rules={[{ required: true, type: 'email', message: '请输入有效邮箱' }]}>
            <Input placeholder="例如：user@example.com" />
          </Form.Item>
          <Form.Item label="角色" name="role" initialValue="member">
            <Select options={[{ value: 'member', label: '普通成员' }, { value: 'admin', label: '管理员' }]} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={inviteLoading}>发送邀请</Button>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default MembersPage;

import { ClearOutlined, UserOutlined, LogoutOutlined } from '@ant-design/icons';
import { Avatar, Dropdown, Modal, Space, message } from 'antd';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

import { useAuth } from '@/modules/iam';
import { memoryUserApi } from '@/modules/memory/api/memory-user.api';

export const UserMenu = () => {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const [clearLoading, setClearLoading] = useState(false);

  if (!user) return <Link to="/login">登录</Link>;

  const handleClearMemories = () => {
    Modal.confirm({
      title: '清空我的记忆',
      content: 'AI 助手将忘记关于你的所有个人记忆，此操作不可恢复，确认继续吗？',
      okText: '确认清空',
      okButtonProps: { danger: true, loading: clearLoading },
      cancelText: '取消',
      onOk: async () => {
        setClearLoading(true);
        try {
          await memoryUserApi.clearMyMemories();
          message.success('记忆已清空');
        } catch {
          message.error('清空失败，请稍后重试');
        } finally {
          setClearLoading(false);
        }
      },
    });
  };

  const items = [
    {
      key: 'clear-memory',
      icon: <ClearOutlined />,
      label: '清空我的记忆',
      onClick: handleClearMemories,
    },
    { type: 'divider' as const },
    {
      key: 'logout',
      icon: <LogoutOutlined />,
      label: '退出登录',
      danger: true,
      onClick: () => {
        logout();
        navigate('/login', { replace: true });
      },
    },
  ];

  return (
    <Dropdown menu={{ items }} placement="bottomRight">
      <Space style={{ cursor: 'pointer' }} size={8}>
        <Avatar
          src={user.avatar_url}
          icon={<UserOutlined />}
          size={28}
          style={{ background: '#1677ff' }}
        />
        <span style={{ fontSize: 13, color: '#262626' }}>{user.github_login}</span>
      </Space>
    </Dropdown>
  );
};

export default UserMenu;

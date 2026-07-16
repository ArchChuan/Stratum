import { ClearOutlined, UserOutlined, LogoutOutlined } from '@ant-design/icons';
import { Avatar, Button, Dropdown, Modal, Space, message } from 'antd';
import { Link, useNavigate } from 'react-router-dom';

import { useAuth } from '@/modules/iam';
import { memoryUserApi } from '@/modules/memory/api/memory-user.api';
import { extractErrorMessage } from '@/shared/lib/errorMessage';

export const UserMenu = () => {
  const { user, logout } = useAuth();
  const navigate = useNavigate();

  if (!user) return <Link to="/login">登录</Link>;

  const handleClearMemories = () => {
    Modal.confirm({
      title: '清空我的记忆',
      content: 'AI 助手将忘记当前账号的所有个人记忆。不会影响其他团队成员，此操作不可恢复。',
      okText: '确认清空',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await memoryUserApi.clearMyMemories();
          message.success('记忆已清空', 2);
        } catch (error) {
          message.error(extractErrorMessage(error, '清空失败，请稍后重试'), 0);
        }
      },
    });
  };

  const items = [
    {
      key: 'clear-memory',
      icon: <ClearOutlined aria-hidden />,
      label: '清空我的记忆',
      onClick: handleClearMemories,
    },
    { type: 'divider' as const },
    {
      key: 'logout',
      icon: <LogoutOutlined aria-hidden />,
      label: '退出登录',
      danger: true,
      onClick: () => {
        logout();
        navigate('/login', { replace: true });
      },
    },
  ];

  return (
    <Dropdown menu={{ items }} placement="bottomRight" trigger={['click']}>
      <Button
        type="text"
        aria-label="打开用户菜单"
        style={{ height: 40, paddingInline: 8 }}
      >
        <Space size={8}>
          <Avatar
            src={user.avatar_url}
            icon={<UserOutlined />}
            size={28}
            style={{ background: '#1677ff' }}
          />
          <span style={{ fontSize: 13, color: '#262626' }}>{user.github_login}</span>
        </Space>
      </Button>
    </Dropdown>
  );
};

export default UserMenu;

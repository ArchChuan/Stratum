import { UserOutlined, LogoutOutlined } from '@ant-design/icons';
import { Avatar, Dropdown, Space } from 'antd';
import { Link, useNavigate } from 'react-router-dom';

import { useAuth } from '@/modules/iam';

export const UserMenu = () => {
  const { user, logout } = useAuth();
  const navigate = useNavigate();

  if (!user) return <Link to="/login">登录</Link>;

  const items = [
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

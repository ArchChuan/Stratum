import { GithubOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Button, Card, Typography, Space, message } from 'antd';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { authApi } from '../../api/auth.api';
import { useAuth } from '../../components/AuthContext';

import { extractErrorMessage } from '@/shared/lib';

const { Title, Text } = Typography;

const handleGithubLogin = () => {
  const base = (import.meta.env.VITE_API_BASE_URL as string) || '';
  window.location.href = `${base}/auth/github`;
};

export const LoginPage = () => {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [guestLoading, setGuestLoading] = useState(false);

  const handleGuestLogin = async () => {
    setGuestLoading(true);
    try {
      const { access_token, tenant_id } = await authApi.guest();
      const me = await authApi.me(access_token);
      login(
        {
          sub: me.sub,
          tenant_id: me.tenant_id || tenant_id,
          role: me.role,
          global_role: me.global_role,
          system_role: me.system_role,
          current_tenant: { id: me.tenant_id || tenant_id, name: '' },
          avatar_url: me.avatar_url || '',
          github_login: me.github_login || '',
        },
        access_token,
      );
      navigate('/', { replace: true });
    } catch (err) {
      message.error(extractErrorMessage(err, '访客登录失败，请稍后重试'));
      setGuestLoading(false);
    }
  };

  return (
    <div
      className="auth-page"
      style={{
        minHeight: '100vh',
      }}
    >
      <Card
        className="auth-card"
        style={{ maxWidth: 380, textAlign: 'center', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}
      >
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <div>
            <Title level={2} style={{ marginBottom: 4 }}>
              Stratum AI
            </Title>
            <Text type="secondary">多租户 AI Agent 平台</Text>
          </div>
          <Button
            type="primary"
            size="large"
            icon={<GithubOutlined />}
            block
            disabled={guestLoading}
            onClick={handleGithubLogin}
          >
            使用 GitHub 登录
          </Button>
          <Button
            size="large"
            icon={<ThunderboltOutlined />}
            block
            loading={guestLoading}
            onClick={handleGuestLogin}
          >
            快速体验（访客）
          </Button>
          <Text type="secondary" style={{ fontSize: 12 }}>
            访客账号临时有效，登录即代表同意服务条款
          </Text>
        </Space>
      </Card>
    </div>
  );
};

export default LoginPage;

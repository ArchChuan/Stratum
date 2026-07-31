import { GithubOutlined, ThunderboltOutlined, UserOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, Tabs, Typography, Space, message } from 'antd';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

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
  const [loginLoading, setLoginLoading] = useState(false);
  const [form] = Form.useForm();

  const handleGuestLogin = async () => {
    setGuestLoading(true);
    try {
      const { access_token, tenant_id, user } = await authApi.guest();
      login(
        {
          ...user,
          tenant_id: user.tenant_id || tenant_id,
          current_tenant: { id: user.tenant_id || tenant_id, name: '' },
        },
        access_token,
      );
      navigate('/', { replace: true });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '访客登录失败，请稍后重试'), duration: 0 });
      setGuestLoading(false);
    }
  };

  const handlePasswordLogin = async (values: { username: string; password: string }) => {
    setLoginLoading(true);
    try {
      const { access_token } = await authApi.passwordLogin(values.username, values.password);
      const me = await authApi.me(access_token);
      login(me, access_token);
      navigate('/', { replace: true });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '用户名或密码错误'), duration: 0 });
    } finally {
      setLoginLoading(false);
    }
  };

  return (
    <div
      className="auth-page"
      style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}
    >
      <Card
        className="auth-card"
        style={{ width: '100%', maxWidth: 380, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}
      >
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={2} style={{ marginBottom: 4 }}>Stratum AI</Title>
          <Text type="secondary">多租户 AI Agent 平台</Text>
        </div>

        <Tabs
          centered
          items={[
            {
              key: 'password',
              label: '账号登录',
              children: (
                <Form form={form} layout="vertical" onFinish={handlePasswordLogin} size="large">
                  <Form.Item
                    name="username"
                    rules={[{ required: true, message: '请输入用户名' }]}
                  >
                    <Input prefix={<UserOutlined />} placeholder="用户名" autoComplete="username" />
                  </Form.Item>
                  <Form.Item
                    name="password"
                    rules={[{ required: true, message: '请输入密码' }]}
                  >
                    <Input.Password prefix={<UserOutlined />} placeholder="密码" autoComplete="current-password" />
                  </Form.Item>
                  <Form.Item>
                    <Button type="primary" htmlType="submit" block loading={loginLoading}>
                      登录
                    </Button>
                  </Form.Item>
                  <div style={{ textAlign: 'center' }}>
                    <Text type="secondary">还没有账号？</Text>{' '}
                    <Link to="/register">立即注册</Link>
                  </div>
                </Form>
              ),
            },
            {
              key: 'third-party',
              label: '第三方登录',
              children: (
                <Space direction="vertical" size="large" style={{ width: '100%' }}>
                  <Button
                    type="primary"
                    size="large"
                    icon={<GithubOutlined />}
                    block
                    disabled={guestLoading || loginLoading}
                    onClick={handleGithubLogin}
                  >
                    使用 GitHub 登录
                  </Button>
                  <Button
                    size="large"
                    icon={<ThunderboltOutlined />}
                    block
                    loading={guestLoading}
                    disabled={loginLoading}
                    onClick={handleGuestLogin}
                  >
                    快速体验（访客）
                  </Button>
                  <Text type="secondary" style={{ fontSize: 12, textAlign: 'center', display: 'block' }}>
                    访客账号临时有效，登录即代表同意服务条款
                  </Text>
                </Space>
              ),
            },
          ]}
        />
      </Card>
    </div>
  );
};

export default LoginPage;

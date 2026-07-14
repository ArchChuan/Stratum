import { Tabs, Form, Input, Button, Card, Typography, message, Space } from 'antd';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { authApi } from '../../api/auth.api';
import { useAuth } from '../../components/AuthContext';

import { extractErrorMessage } from '@/shared/lib';

const { Title, Text } = Typography;

export const OnboardingPage = () => {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [createLoading, setCreateLoading] = useState(false);
  const [joinLoading, setJoinLoading] = useState(false);

  const getOnboardingToken = (): string | null => {
    const token = sessionStorage.getItem('onboarding_token');
    if (!token) {
      message.error('登录已过期，请重新登录');
      navigate('/login', { replace: true });
      return null;
    }
    return token;
  };

  const finishLogin = async (accessToken: string, tenantId: string) => {
    sessionStorage.removeItem('onboarding_token');
    sessionStorage.removeItem('github_login');
    sessionStorage.removeItem('avatar_url');
    login(
      {
        tenant_id: tenantId,
        current_tenant: { id: tenantId, name: '' },
        avatar_url: '',
        github_login: '',
      },
      accessToken,
    );
    try {
      const me = await authApi.me();
      login(
        {
          sub: me.sub,
          tenant_id: me.tenant_id,
          role: me.role,
          global_role: me.global_role,
          system_role: me.system_role,
          current_tenant: { id: me.tenant_id || tenantId, name: '' },
          avatar_url: me.avatar_url || '',
          github_login: me.github_login || '',
        },
        accessToken,
      );
    } catch {
      /* /auth/me failed but token valid; proceed */
    }
    navigate('/', { replace: true });
  };

  const handleCreate = async (values: { name: string }) => {
    const onboardingToken = getOnboardingToken();
    if (!onboardingToken) return;
    setCreateLoading(true);
    try {
      const res = await authApi.register({
        onboarding_token: onboardingToken,
        action: 'create',
        tenant_name: values.name,
      });
      message.success('租户创建成功！');
      await finishLogin(res.access_token, res.tenant_id);
    } catch (err) {
      message.error(extractErrorMessage(err, '创建失败'));
    } finally {
      setCreateLoading(false);
    }
  };

  const handleJoin = async (values: { invite_code: string }) => {
    const onboardingToken = getOnboardingToken();
    if (!onboardingToken) return;
    setJoinLoading(true);
    try {
      const res = await authApi.register({
        onboarding_token: onboardingToken,
        action: 'join',
        invitation_token: values.invite_code,
      });
      message.success('加入成功！');
      await finishLogin(res.access_token, res.tenant_id);
    } catch (err) {
      message.error(extractErrorMessage(err, '加入失败，邀请码无效或已过期'));
    } finally {
      setJoinLoading(false);
    }
  };

  const tabItems = [
    {
      key: 'create',
      label: '创建新租户',
      children: (
        <Form layout="vertical" onFinish={handleCreate}>
          <Form.Item
            label="租户名称"
            name="name"
            rules={[{ required: true, message: '请输入租户名称' }]}
          >
            <Input placeholder="例如：我的团队" maxLength={64} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={createLoading}>
              创建租户
            </Button>
          </Form.Item>
        </Form>
      ),
    },
    {
      key: 'join',
      label: '加入已有租户',
      children: (
        <Form layout="vertical" onFinish={handleJoin}>
          <Form.Item
            label="邀请码"
            name="invite_code"
            rules={[{ required: true, message: '请输入邀请码' }]}
          >
            <Input placeholder="粘贴管理员给您的邀请码" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={joinLoading}>
              加入租户
            </Button>
          </Form.Item>
        </Form>
      ),
    },
  ];

  return (
    <div
      className="auth-page"
      style={{
        minHeight: '100vh',
      }}
    >
      <Card className="auth-card" style={{ width: '100%', maxWidth: 440, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <div style={{ textAlign: 'center' }}>
            <Title level={3} style={{ marginBottom: 4 }}>
              欢迎使用 Stratum
            </Title>
            <Text type="secondary">创建您的租户空间，或加入已有团队</Text>
          </div>
          <Tabs defaultActiveKey="create" items={tabItems} />
        </Space>
      </Card>
    </div>
  );
};

export default OnboardingPage;

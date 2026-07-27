import { Tabs, Form, Input, Button, Card, Typography, message, Space } from 'antd';
import { useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

import { authApi } from '../../api/auth.api';
import { tenantApi } from '../../api/tenant.api';
import { useAuth } from '../../components/AuthContext';

import { extractErrorMessage } from '@/shared/lib';

const { Title, Text } = Typography;

export const OnboardingPage = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const { login, user, switchTenant } = useAuth();
  const [createLoading, setCreateLoading] = useState(false);
  const [joinLoading, setJoinLoading] = useState(false);

  const onboardingState = location.state as {
    onboardingToken?: string;
    githubLogin?: string;
    avatarURL?: string;
  } | null;

  const getOnboardingToken = (): string | null => onboardingState?.onboardingToken || null;

  const finishLogin = async (accessToken: string, tenantId: string) => {
    login(
      {
        tenant_id: tenantId,
        current_tenant: { id: tenantId, name: '' },
        avatar_url: onboardingState?.avatarURL || '',
        github_login: onboardingState?.githubLogin || '',
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
    setCreateLoading(true);
    try {
      if (onboardingToken) {
        const res = await authApi.register({
          onboarding_token: onboardingToken,
          action: 'create',
          tenant_name: values.name,
        });
        await finishLogin(res.access_token, res.tenant_id);
      } else if (user?.sub) {
        const res = await authApi.createUserTenant(values.name);
        await switchTenant(res.tenant_id);
        navigate('/', { replace: true });
      } else {
        navigate('/login', { replace: true });
        return;
      }
      message.success({ content: '租户创建成功', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '创建失败'), duration: 0 });
    } finally {
      setCreateLoading(false);
    }
  };

  const handleJoin = async (values: { invite_code: string }) => {
    const onboardingToken = getOnboardingToken();
    setJoinLoading(true);
    try {
      if (onboardingToken) {
        const res = await authApi.register({
          onboarding_token: onboardingToken,
          action: 'join',
          invitation_token: values.invite_code,
        });
        await finishLogin(res.access_token, res.tenant_id);
      } else if (user?.sub) {
        const res = await tenantApi.joinExisting(values.invite_code);
        await switchTenant(res.tenant_id);
        navigate('/', { replace: true });
      } else {
        navigate('/login', { replace: true });
        return;
      }
      message.success({ content: '加入成功', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '加入失败，邀请码无效或已过期'), duration: 0 });
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

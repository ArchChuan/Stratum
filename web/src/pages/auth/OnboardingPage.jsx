import React, { useState } from 'react';
import { Tabs, Form, Input, Button, Card, Typography, message, Space } from 'antd';
import { useNavigate } from 'react-router-dom';
import { postRegister, getMe } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Title, Text } = Typography;

const OnboardingPage = () => {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [createLoading, setCreateLoading] = useState(false);
  const [joinLoading, setJoinLoading] = useState(false);

  const getOnboardingToken = () => {
    const token = sessionStorage.getItem('onboarding_token');
    if (!token) {
      message.error('登录已过期，请重新登录');
      navigate('/login', { replace: true });
    }
    return token;
  };

  const finishLogin = async (access_token, tenant_id) => {
    sessionStorage.removeItem('onboarding_token');
    sessionStorage.removeItem('github_login');
    sessionStorage.removeItem('avatar_url');
    // Set token first so the /auth/me request carries the Authorization header.
    login({ tenant_id, current_tenant: { id: tenant_id } }, access_token);
    try {
      const meRes = await getMe();
      login(
        {
          sub: meRes.data.sub,
          tenant_id: meRes.data.tenant_id,
          role: meRes.data.role,
          global_role: meRes.data.global_role,
          current_tenant: { id: meRes.data.tenant_id },
          avatar_url: meRes.data.avatar_url || '',
          github_login: meRes.data.github_login || '',
        },
        access_token,
      );
    } catch {
      // /auth/me failed but we have a valid access_token; navigate anyway.
    }
    navigate('/', { replace: true });
  };

  const handleCreate = async (values) => {
    const onboardingToken = getOnboardingToken();
    if (!onboardingToken) return;
    setCreateLoading(true);
    try {
      const res = await postRegister({
        onboarding_token: onboardingToken,
        action: 'create',
        tenant_name: values.name,
      });
      message.success('租户创建成功！');
      await finishLogin(res.data.access_token, res.data.tenant_id);
    } catch (err) {
      message.error(err.response?.data?.error || '创建失败');
    } finally {
      setCreateLoading(false);
    }
  };

  const handleJoin = async (values) => {
    const onboardingToken = getOnboardingToken();
    if (!onboardingToken) return;
    setJoinLoading(true);
    try {
      const res = await postRegister({
        onboarding_token: onboardingToken,
        action: 'join',
        invitation_token: values.invite_code,
      });
      message.success('加入成功！');
      await finishLogin(res.data.access_token, res.data.tenant_id);
    } catch (err) {
      message.error(err.response?.data?.error || '加入失败，邀请码无效或已过期');
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
          <Form.Item label="租户名称" name="name" rules={[{ required: true, message: '请输入租户名称' }]}>
            <Input placeholder="例如：我的团队" maxLength={64} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={createLoading}>创建租户</Button>
          </Form.Item>
        </Form>
      ),
    },
    {
      key: 'join',
      label: '加入已有租户',
      children: (
        <Form layout="vertical" onFinish={handleJoin}>
          <Form.Item label="邀请码" name="invite_code" rules={[{ required: true, message: '请输入邀请码' }]}>
            <Input placeholder="粘贴管理员给您的邀请码" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={joinLoading}>加入租户</Button>
          </Form.Item>
        </Form>
      ),
    },
  ];

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#f0f2f5' }}>
      <Card style={{ width: 440, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <div style={{ textAlign: 'center' }}>
            <Title level={3} style={{ marginBottom: 4 }}>欢迎使用 Stratum</Title>
            <Text type="secondary">创建您的租户空间，或加入已有团队</Text>
          </div>
          <Tabs defaultActiveKey="create" items={tabItems} />
        </Space>
      </Card>
    </div>
  );
};

export default OnboardingPage;

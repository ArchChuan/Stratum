import { Button, Card, Form, Input, Typography, message } from 'antd';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

import { authApi } from '../../api/auth.api';
import { useAuth } from '../../components/AuthContext';

import { extractErrorMessage } from '@/shared/lib';

const { Title, Text } = Typography;

export const RegisterPage = () => {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [registering, setRegistering] = useState(false);
  const [form] = Form.useForm();

  const handleRegister = async (values: {
    username: string;
    password: string;
    confirm: string;
  }) => {
    if (values.password !== values.confirm) {
      message.error({ content: '两次输入的密码不一致', duration: 3 });
      return;
    }
    setRegistering(true);
    try {
      const { access_token } = await authApi.passwordRegister(
        values.username,
        values.password,
      );
      const me = await authApi.me(access_token);
      login(me, access_token);
      navigate('/', { replace: true });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '注册失败，请稍后重试'), duration: 3 });
    } finally {
      setRegistering(false);
    }
  };

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Card style={{ width: '100%', maxWidth: 380, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={2} style={{ marginBottom: 4 }}>Stratum AI</Title>
          <Text type="secondary">创建账号</Text>
        </div>
        <Form form={form} layout="vertical" onFinish={handleRegister} size="large">
          <Form.Item
            name="username"
            rules={[
              { required: true, message: '请输入用户名' },
              { min: 3, message: '至少3个字符' },
              { max: 32, message: '最多32个字符' },
              { pattern: /^[a-zA-Z0-9_]+$/, message: '仅支持字母、数字和下划线' },
            ]}
          >
            <Input placeholder="用户名" autoComplete="username" />
          </Form.Item>
          <Form.Item
            name="password"
            rules={[
              { required: true, message: '请输入密码' },
              { min: 8, message: '密码至少8位，需包含大小写字母和数字' },
              { pattern: /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d)/, message: '需包含大写、小写字母和数字' },
            ]}
          >
            <Input.Password placeholder="密码" autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm"
            dependencies={['password']}
            rules={[
              { required: true, message: '请确认密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('password') === value) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error('两次输入的密码不一致'));
                },
              }),
            ]}
          >
            <Input.Password placeholder="确认密码" autoComplete="new-password" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={registering}>
              注册
            </Button>
          </Form.Item>
        </Form>
        <div style={{ textAlign: 'center' }}>
          <Text type="secondary">已有账号？</Text>{' '}
          <Link to="/login">立即登录</Link>
        </div>
      </Card>
    </div>
  );
};

export default RegisterPage;

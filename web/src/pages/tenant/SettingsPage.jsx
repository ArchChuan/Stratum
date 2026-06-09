import React, { useState, useEffect, useCallback } from 'react';
import { Form, Input, Button, Typography, message, Card, Space, Divider, Spin } from 'antd';
import { EyeInvisibleOutlined, EyeTwoTone, CheckCircleFilled } from '@ant-design/icons';
import { getTenantSettings, updateTenant } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Title, Text } = Typography;

const PROVIDERS = [
  { key: 'qwen',  label: '通义千问 (Qwen)' },
  { key: 'zhipu', label: '智谱 AI (Zhipu)' },
];

const SettingsPage = () => {
  const { user, login, accessToken } = useAuth();
  const [basicForm] = Form.useForm();
  const [keyForm] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [keyLoading, setKeyLoading] = useState(false);
  const [fetchLoading, setFetchLoading] = useState(true);
  const [maskedKeys, setMaskedKeys] = useState({});

  const role = user?.current_tenant?.role || user?.role;
  const canEditKeys = role === 'owner' || role === 'admin';

  const loadSettings = useCallback(async () => {
    try {
      const res = await getTenantSettings();
      const apiKeys = res.data?.settings?.llm_api_keys || {};
      // Store masked values separately; don't pre-fill the form fields so
      // the user must type a new value to update (same as GitHub/Stripe).
      setMaskedKeys(apiKeys);
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '加载设置失败');
    } finally {
      setFetchLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  const handleBasicSave = async (values) => {
    setLoading(true);
    try {
      await updateTenant(values);
      message.success('设置已保存');
      login({ ...user, current_tenant: { ...user.current_tenant, ...values } }, accessToken);
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '保存失败');
    } finally {
      setLoading(false);
    }
  };

  const handleKeySave = async (values) => {
    const llm_api_keys = {};
    PROVIDERS.forEach(({ key }) => {
      if (values[key]) llm_api_keys[key] = values[key];
    });
    if (Object.keys(llm_api_keys).length === 0) {
      message.warning('请输入至少一个 API Key');
      return;
    }
    setKeyLoading(true);
    try {
      await updateTenant({ settings: { llm_api_keys } });
      keyForm.resetFields();
      message.success('API Key 已保存');
      await loadSettings();
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '保存失败');
    } finally {
      setKeyLoading(false);
    }
  };

  return (
    <div>
      <Title level={4} style={{ marginBottom: 24 }}>租户设置</Title>

      <Card style={{ maxWidth: 520, marginBottom: 24 }}>
        <Form
          form={basicForm}
          layout="vertical"
          initialValues={{
            name: user?.current_tenant?.name || '',
            avatar_url: user?.current_tenant?.avatar_url || '',
          }}
          onFinish={handleBasicSave}
        >
          <Form.Item label="租户名称" name="name" rules={[{ required: true, message: '请输入租户名称' }]}>
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading}>保存</Button>
          </Form.Item>
        </Form>
      </Card>

      <Card
        title="LLM API Key 配置"
        style={{ maxWidth: 520 }}
        extra={!canEditKeys && <Text type="secondary">仅 owner/admin 可编辑</Text>}
      >
        <Spin spinning={fetchLoading}>
          <Form form={keyForm} layout="vertical" onFinish={handleKeySave}>
            {PROVIDERS.map(({ key, label }) => (
              <Form.Item
                key={key}
                label={label}
                name={key}
                extra={maskedKeys[key] ? (
                  <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>
                    <CheckCircleFilled style={{ color: '#52c41a', marginRight: 4 }} />
                    {maskedKeys[key]}
                  </Text>
                ) : undefined}
              >
                <Input.Password
                  placeholder={canEditKeys ? '输入新值以覆盖，留空则不更改' : '无权限修改'}
                  disabled={!canEditKeys}
                  iconRender={(visible) => (visible ? <EyeTwoTone /> : <EyeInvisibleOutlined />)}
                />
              </Form.Item>
            ))}
            <Divider style={{ margin: '8px 0 16px' }} />
            <Space>
              <Button
                type="primary"
                htmlType="submit"
                loading={keyLoading}
                disabled={!canEditKeys}
              >
                保存 API Key
              </Button>
              {!canEditKeys && (
                <Text type="secondary" style={{ fontSize: 12 }}>当前角色（{role}）无权限修改</Text>
              )}
            </Space>
          </Form>
        </Spin>
      </Card>
    </div>
  );
};

export default SettingsPage;

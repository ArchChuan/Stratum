import React, { useState, useEffect, useCallback } from 'react';
import { Form, Input, Button, Typography, message, Space, Divider, Spin, Select, Tag, Row, Col } from 'antd';
import { EyeInvisibleOutlined, EyeTwoTone, CheckCircleFilled, SettingOutlined, KeyOutlined, DatabaseOutlined } from '@ant-design/icons';
import { getTenantSettings, updateTenant } from '../../services/api';
import { setTenantEmbedModel } from '../../services/tenant';
import { useAuth } from '../../hooks/useAuth';

const { Title, Text } = Typography;

const PROVIDERS = [
  { key: 'qwen',  label: '通义千问 (Qwen)' },
  { key: 'zhipu', label: '智谱 AI (Zhipu)' },
];

import { EMBEDDING_MODEL_OPTIONS } from '../../constants';

const SectionHeader = ({ icon, title, subtitle }) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
    <div style={{
      width: 32, height: 32, borderRadius: 8,
      background: '#f0f5ff', display: 'flex', alignItems: 'center', justifyContent: 'center',
    }}>
      {React.cloneElement(icon, { style: { color: '#2f54eb', fontSize: 16 } })}
    </div>
    <div>
      <Text strong style={{ fontSize: 14, display: 'block' }}>{title}</Text>
      {subtitle && <Text type="secondary" style={{ fontSize: 12 }}>{subtitle}</Text>}
    </div>
  </div>
);

const SettingsPage = () => {
  const { user, login, accessToken } = useAuth();
  const [basicForm] = Form.useForm();
  const [keyForm] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [keyLoading, setKeyLoading] = useState(false);
  const [fetchLoading, setFetchLoading] = useState(true);
  const [maskedKeys, setMaskedKeys] = useState({});
  const [embedModel, setEmbedModel] = useState('');
  const [embedLoading, setEmbedLoading] = useState(false);
  const [selectedEmbedModel, setSelectedEmbedModel] = useState('');

  const role = user?.current_tenant?.role || user?.role;
  const canEditKeys = role === 'owner' || role === 'admin';

  const loadSettings = useCallback(async () => {
    try {
      const res = await getTenantSettings();
      const apiKeys = res.data?.settings?.llm_api_keys || {};
      setMaskedKeys(apiKeys);
      setEmbedModel(res.data?.settings?.embed_model || '');
    } catch (err) {
      if (err.response?.status !== 403) message.error(err.response?.data?.message || '加载设置失败');
    } finally {
      setFetchLoading(false);
    }
  }, []);

  useEffect(() => { loadSettings(); }, [loadSettings]);

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

  const handleEmbedSave = async () => {
    if (!selectedEmbedModel) {
      message.warning('请选择嵌入模型');
      return;
    }
    setEmbedLoading(true);
    try {
      await setTenantEmbedModel(selectedEmbedModel);
      setEmbedModel(selectedEmbedModel);
      message.success('嵌入模型已设置');
    } catch (err) {
      if (err.response?.status === 400) {
        message.error(err.response?.data?.message || '嵌入模型已设置且不可更改');
      } else if (err.response?.status !== 403) {
        message.error(err.response?.data?.message || '设置失败');
      }
    } finally {
      setEmbedLoading(false);
    }
  };

  const handleKeySave = async (values) => {    const llm_api_keys = {};
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
    <div style={{ maxWidth: 960 }}>
      <div style={{ marginBottom: 24 }}>
        <Title level={4} style={{ margin: 0 }}>租户设置</Title>
        <Text type="secondary" style={{ fontSize: 13 }}>管理租户基本信息与 LLM 接口配置</Text>
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {/* 基本信息 */}
        <Col xs={24} md={12} xl={10}>
          <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, height: '100%' }}>
            <SectionHeader icon={<SettingOutlined />} title="基本信息" subtitle="租户名称等基础配置" />
            <Form
              form={basicForm}
              layout="vertical"
              initialValues={{ name: user?.current_tenant?.name || '' }}
              onFinish={handleBasicSave}
            >
              <Form.Item label="租户名称" name="name" rules={[{ required: true, message: '请输入租户名称' }]} style={{ marginBottom: 0 }}>
                <Input maxLength={64} />
              </Form.Item>
              <div style={{ marginTop: 16 }}>
                <Button type="primary" htmlType="submit" loading={loading}>保存</Button>
              </div>
            </Form>
          </div>
        </Col>

        {/* 嵌入模型配置 */}
        <Col xs={24} md={12} xl={14}>
          <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, height: '100%' }}>
            <SectionHeader icon={<DatabaseOutlined />} title="嵌入模型" subtitle="记忆系统向量化使用的 Embedding 模型，设置后不可更改" />
            <Spin spinning={fetchLoading}>
              {embedModel ? (
                <div>
                  <Text type="secondary" style={{ fontSize: 13 }}>当前嵌入模型：</Text>
                  <Tag color="blue" style={{ marginLeft: 8, fontSize: 13 }}>{embedModel}</Tag>
                  <div style={{ marginTop: 8 }}>
                    <Text type="secondary" style={{ fontSize: 12 }}>嵌入模型已设置，不支持修改（修改需重建所有向量索引）</Text>
                  </div>
                </div>
              ) : (
                <div>
                  <Text type="secondary" style={{ fontSize: 13, display: 'block', marginBottom: 12 }}>
                    尚未配置嵌入模型，设置后将用于记忆与知识库的向量化，<Text strong style={{ color: '#fa8c16' }}>设置后不可更改</Text>
                  </Text>
                  <Space wrap>
                    <Select
                      style={{ width: 300 }}
                      placeholder="选择嵌入模型"
                      value={selectedEmbedModel || undefined}
                      onChange={setSelectedEmbedModel}
                      disabled={!canEditKeys}
                      options={EMBEDDING_MODEL_OPTIONS}
                    />
                    <Button
                      type="primary"
                      loading={embedLoading}
                      disabled={!canEditKeys || !selectedEmbedModel}
                      onClick={handleEmbedSave}
                    >
                      确认设置
                    </Button>
                  </Space>
                  {!canEditKeys && (
                    <div style={{ marginTop: 8 }}>
                      <Text type="secondary" style={{ fontSize: 12 }}>当前角色（{role}）无权限修改</Text>
                    </div>
                  )}
                </div>
              )}
            </Spin>
          </div>
        </Col>
      </Row>

      {/* API Key 配置 */}
      <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24 }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 0 }}>
          <SectionHeader icon={<KeyOutlined />} title="LLM API Key" subtitle="配置各 LLM 提供商的接口密钥" />
          {!canEditKeys && <Text type="secondary" style={{ fontSize: 12 }}>仅 owner / admin 可编辑</Text>}
        </div>

        <Spin spinning={fetchLoading}>
          <Form form={keyForm} layout="vertical" onFinish={handleKeySave}>
            <Row gutter={24}>
              {PROVIDERS.map(({ key, label }) => (
                <Col key={key} xs={24} md={12}>
                  <Form.Item
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
                </Col>
              ))}
            </Row>
            <Divider style={{ margin: '0 0 16px' }} />
            <Space>
              <Button type="primary" htmlType="submit" loading={keyLoading} disabled={!canEditKeys}>
                保存 API Key
              </Button>
              {!canEditKeys && (
                <Text type="secondary" style={{ fontSize: 12 }}>当前角色（{role}）无权限修改</Text>
              )}
            </Space>
          </Form>
        </Spin>
      </div>
    </div>
  );
};

export default SettingsPage;

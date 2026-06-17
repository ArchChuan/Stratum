import {
  CheckCircleFilled,
  EyeInvisibleOutlined,
  EyeTwoTone,
  KeyOutlined,
} from '@ant-design/icons';
import { Button, Col, Divider, Form, Input, Row, Space, Spin, Typography } from 'antd';

import { SectionHeader } from '@/shared/ui';

const { Text } = Typography;

const PROVIDERS = [
  { key: 'qwen', label: '通义千问 (Qwen)' },
  { key: 'zhipu', label: '智谱 AI (Zhipu)' },
];

interface Props {
  maskedKeys: Record<string, string>;
  fetchLoading: boolean;
  keyLoading: boolean;
  canEditKeys: boolean;
  role?: string;
  onSave: (llm_api_keys: Record<string, string>) => Promise<void> | void;
}

export const TenantApiKeyCard = ({
  maskedKeys,
  fetchLoading,
  keyLoading,
  canEditKeys,
  role,
  onSave,
}: Props) => {
  const [form] = Form.useForm<Record<string, string>>();

  const handleFinish = async (values: Record<string, string>) => {
    const llm_api_keys: Record<string, string> = {};
    PROVIDERS.forEach(({ key }) => {
      if (values[key]) llm_api_keys[key] = values[key];
    });
    await onSave(llm_api_keys);
    if (Object.keys(llm_api_keys).length > 0) form.resetFields();
  };

  return (
    <div style={{ background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          marginBottom: 0,
        }}
      >
        <SectionHeader
          icon={<KeyOutlined />}
          title="LLM API Key"
          subtitle="配置各 LLM 提供商的接口密钥"
        />
        {!canEditKeys && (
          <Text type="secondary" style={{ fontSize: 12 }}>
            仅 owner / admin 可编辑
          </Text>
        )}
      </div>

      <Spin spinning={fetchLoading}>
        <Form form={form} layout="vertical" onFinish={handleFinish}>
          <Row gutter={24}>
            {PROVIDERS.map(({ key, label }) => (
              <Col key={key} xs={24} md={12}>
                <Form.Item
                  label={label}
                  name={key}
                  extra={
                    maskedKeys[key] ? (
                      <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>
                        <CheckCircleFilled style={{ color: '#52c41a', marginRight: 4 }} />
                        {maskedKeys[key]}
                      </Text>
                    ) : undefined
                  }
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
              <Text type="secondary" style={{ fontSize: 12 }}>
                当前角色（{role}）无权限修改
              </Text>
            )}
          </Space>
        </Form>
      </Spin>
    </div>
  );
};

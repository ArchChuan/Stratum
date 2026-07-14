import { ReloadOutlined } from '@ant-design/icons';
import { Col, Form, InputNumber, Row, Switch, Typography } from 'antd';

import { mcpSectionStyle } from './mcpFormStyles';

import { SectionHeader } from '@/shared/ui';


const { Text } = Typography;

interface Props {
  retryEnabled?: boolean;
}

export const MCPRetrySection = ({ retryEnabled }: Props) => (
  <div style={mcpSectionStyle}>
    <SectionHeader icon={<ReloadOutlined />} title="重连配置" subtitle="断连后自动重试策略" />
    <Form.Item
      label="启用自动重连"
      name="retry_enabled"
      valuePropName="checked"
      style={retryEnabled ? undefined : { marginBottom: 0 }}
    >
      <Switch />
    </Form.Item>

    {retryEnabled && (
      <>
        <div
          style={{
            background: '#f6ffed',
            border: '1px solid #b7eb8f',
            borderRadius: 8,
            padding: '10px 14px',
            marginBottom: 16,
          }}
        >
          <Text type="secondary" style={{ fontSize: 12 }}>
            等待时间 = min(初始延迟 × 退避系数^n, 最大延迟)，指数退避直至超过最大重试次数
          </Text>
        </div>
        <Row gutter={16}>
          <Col xs={24} sm={12} md={6}>
            <Form.Item
              label="最大重试次数"
              name="retry_max_retries"
              rules={[{ required: true }]}
            >
              <InputNumber min={1} max={20} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={6}>
            <Form.Item
              label="初始延迟（ms）"
              name="retry_initial_delay_ms"
              rules={[{ required: true }]}
            >
              <InputNumber min={100} max={60000} step={100} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={6}>
            <Form.Item
              label="最大延迟（ms）"
              name="retry_max_delay_ms"
              rules={[{ required: true }]}
            >
              <InputNumber min={1000} max={300000} step={1000} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col xs={24} sm={12} md={6}>
            <Form.Item
              label="退避系数"
              name="retry_backoff_factor"
              rules={[{ required: true }]}
              style={{ marginBottom: 0 }}
            >
              <InputNumber min={1.0} max={10.0} step={0.5} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
      </>
    )}
  </div>
);

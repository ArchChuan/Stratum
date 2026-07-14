import { SafetyOutlined } from '@ant-design/icons';
import { Alert, Col, Form, Input, Row, Select } from 'antd';

import { mcpSectionStyle } from './mcpFormStyles';

import { SectionHeader } from '@/shared/ui';


const { Option } = Select;

interface Props {
  authType?: string;
}

export const MCPAuthSection = ({ authType }: Props) => (
  <div style={mcpSectionStyle}>
    <SectionHeader
      icon={<SafetyOutlined />}
      title="认证配置"
      subtitle="连接远程服务器时的身份验证方式"
    />
    <Form.Item
      label="认证类型"
      name="auth_type"
      style={authType === 'none' ? { marginBottom: 0 } : undefined}
    >
      <Select>
        <Option value="none">无认证</Option>
        <Option value="bearer">Bearer Token</Option>
        <Option value="api_key">API Key 请求头</Option>
        <Option value="oauth2">OAuth2 客户端凭证</Option>
      </Select>
    </Form.Item>

    {authType === 'bearer' && (
      <Form.Item
        label="Bearer Token"
        name="bearer_token"
        rules={[{ required: true, message: '请输入 token' }]}
        style={{ marginBottom: 0 }}
      >
        <Input.Password placeholder="输入 Bearer Token" />
      </Form.Item>
    )}

    {authType === 'api_key' && (
      <Row gutter={16}>
        <Col xs={24} md={8}>
          <Form.Item
            label="请求头名称"
            name="api_key_header"
            initialValue="X-API-Key"
            rules={[{ required: true }]}
            style={{ marginBottom: 0 }}
          >
            <Input placeholder="X-API-Key" />
          </Form.Item>
        </Col>
        <Col xs={24} md={16}>
          <Form.Item
            label="API Key 值"
            name="api_key_value"
            rules={[{ required: true, message: '请输入 API Key' }]}
            style={{ marginBottom: 0 }}
          >
            <Input.Password placeholder="sk-..." />
          </Form.Item>
        </Col>
      </Row>
    )}

    {authType === 'oauth2' && (
      <>
        <Alert
          message="OAuth2 客户端凭证流程（Client Credentials）"
          description="服务端将使用 client_id / client_secret 向 token_url 获取访问令牌，并在每次请求时附加 Authorization: Bearer <token>。"
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
        />
        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item label="Client ID" name="oauth2_client_id" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              label="Client Secret"
              name="oauth2_client_secret"
              rules={[{ required: true }]}
            >
              <Input.Password />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col xs={24} md={16}>
            <Form.Item label="Token URL" name="oauth2_token_url" rules={[{ required: true }]}>
              <Input placeholder="https://auth.example.com/oauth2/token" />
            </Form.Item>
          </Col>
          <Col xs={24} md={8}>
            <Form.Item
              label="Scopes"
              name="oauth2_scopes"
              extra="逗号或空格分隔"
              style={{ marginBottom: 0 }}
            >
              <Input placeholder="mcp.read mcp.write" />
            </Form.Item>
          </Col>
        </Row>
      </>
    )}
  </div>
);

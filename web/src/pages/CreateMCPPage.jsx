import React from 'react';
import {
  Form, Input, Select, InputNumber, Switch, Button,
  Typography, Alert, Row, Col,
} from 'antd';
import {
  ArrowLeftOutlined, ApiOutlined, LinkOutlined, SafetyOutlined, ReloadOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import {
  MCP_DEFAULT_TIMEOUT_SEC, MCP_MAX_TIMEOUT_SEC,
  MCP_RETRY_INITIAL_DELAY_MS, MCP_RETRY_MAX_DELAY_MS,
  MCP_RETRY_MAX_RETRIES, MCP_RETRY_BACKOFF_FACTOR,
} from '../constants';
import useCreateMCPPage from '../hooks/useCreateMCPPage';

const { Title, Text } = Typography;
const { Option } = Select;
const { TextArea } = Input;

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

const TRANSPORT_DESCS = {
  stdio: '通过标准输入/输出通信，适合本地命令行工具',
  sse: '通过 HTTP SSE 长连接通信，适合远程 MCP 服务',
  http: '通过 HTTP 请求/响应通信，适合无状态远程服务',
  'streamable-http': '流式 HTTP，MCP 2025-03-26 规范推荐方式',
};

const sectionStyle = { background: '#fff', borderRadius: 12, border: '1px solid #f0f0f0', padding: 24, marginBottom: 16 };

export default function CreateMCPPage() {
  const [form] = Form.useForm();
  const { submitting, handleFinish } = useCreateMCPPage();
  const navigate = useNavigate();

  const transport = Form.useWatch('transport', form);
  const authType = Form.useWatch('auth_type', form);
  const retryEnabled = Form.useWatch('retry_enabled', form);
  const isHTTP = transport && transport !== 'stdio';

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/mcp')} type="text">返回</Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>添加 MCP 服务器</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>配置外部工具服务器连接</Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={{
          transport: 'stdio',
          auth_type: 'none',
          timeout_sec: MCP_DEFAULT_TIMEOUT_SEC,
          retry_enabled: false,
          retry_max_retries: MCP_RETRY_MAX_RETRIES,
          retry_initial_delay_ms: MCP_RETRY_INITIAL_DELAY_MS,
          retry_max_delay_ms: MCP_RETRY_MAX_DELAY_MS,
          retry_backoff_factor: MCP_RETRY_BACKOFF_FACTOR,
        }}
      >
        {/* 基本信息 */}
        <div style={sectionStyle}>
          <SectionHeader icon={<ApiOutlined />} title="基本信息" />
          <Row gutter={16}>
            <Col span={14}>
              <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入服务器名称' }]}>
                <Input placeholder="例如：filesystem-server" maxLength={64} />
              </Form.Item>
            </Col>
            <Col span={5}>
              <Form.Item label="版本" name="version">
                <Input placeholder="1.0.0" maxLength={32} />
              </Form.Item>
            </Col>
            <Col span={5}>
              <Form.Item label="超时（秒）" name="timeout_sec" rules={[{ required: true }]}>
                <InputNumber min={1} max={MCP_MAX_TIMEOUT_SEC} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item label="传输协议" name="transport" rules={[{ required: true }]} style={{ marginBottom: 0 }}>
            <Select>
              {Object.entries(TRANSPORT_DESCS).map(([k, desc]) => (
                <Option key={k} value={k}>
                  <Text strong style={{ marginRight: 6 }}>{k}</Text>
                  <Text type="secondary" style={{ fontSize: 12 }}>— {desc}</Text>
                </Option>
              ))}
            </Select>
          </Form.Item>
        </div>

        {/* 连接配置 */}
        <div style={sectionStyle}>
          <SectionHeader
            icon={<LinkOutlined />}
            title="连接配置"
            subtitle={transport === 'stdio' ? '本地进程启动参数' : '远程服务器地址与请求头'}
          />
          {transport === 'stdio' ? (
            <>
              <Form.Item label="命令" name="command" rules={[{ required: true, message: '请输入启动命令' }]}>
                <Input placeholder="例如：npx 或 /usr/bin/python3" />
              </Form.Item>
              <Form.Item label="参数" name="args" extra="空格分隔，例如：-m mcp_server --port 8080">
                <Input placeholder="mcp-server-filesystem /home/user/projects" />
              </Form.Item>
              <Form.Item label="环境变量" name="env" extra="每行一个，格式：KEY=VALUE" style={{ marginBottom: 0 }}>
                <TextArea rows={4} placeholder={'ANTHROPIC_API_KEY=sk-xxx\nDATA_DIR=/tmp/data'} style={{ fontFamily: 'monospace', fontSize: 13 }} />
              </Form.Item>
            </>
          ) : (
            <>
              <Form.Item label="服务器 URL" name="url" rules={[{ required: true, message: '请输入 URL' }]}>
                <Input placeholder="https://mcp.example.com/sse" />
              </Form.Item>
              <Form.Item label="自定义请求头" name="headers" extra="每行一个，格式：Header-Name: value" style={{ marginBottom: 0 }}>
                <TextArea rows={3} placeholder={'X-Custom-Header: value\nX-Request-Source: stratum'} style={{ fontFamily: 'monospace', fontSize: 13 }} />
              </Form.Item>
            </>
          )}
        </div>

        {/* 认证配置（仅 HTTP 类型） */}
        {isHTTP && (
          <div style={sectionStyle}>
            <SectionHeader icon={<SafetyOutlined />} title="认证配置" subtitle="连接远程服务器时的身份验证方式" />
            <Form.Item label="认证类型" name="auth_type" style={authType === 'none' ? { marginBottom: 0 } : undefined}>
              <Select>
                <Option value="none">无认证</Option>
                <Option value="bearer">Bearer Token</Option>
                <Option value="api_key">API Key 请求头</Option>
                <Option value="oauth2">OAuth2 客户端凭证</Option>
              </Select>
            </Form.Item>

            {authType === 'bearer' && (
              <Form.Item label="Bearer Token" name="bearer_token" rules={[{ required: true, message: '请输入 token' }]} style={{ marginBottom: 0 }}>
                <Input.Password placeholder="输入 Bearer Token" />
              </Form.Item>
            )}

            {authType === 'api_key' && (
              <Row gutter={16}>
                <Col span={8}>
                  <Form.Item label="请求头名称" name="api_key_header" initialValue="X-API-Key" rules={[{ required: true }]} style={{ marginBottom: 0 }}>
                    <Input placeholder="X-API-Key" />
                  </Form.Item>
                </Col>
                <Col span={16}>
                  <Form.Item label="API Key 值" name="api_key_value" rules={[{ required: true, message: '请输入 API Key' }]} style={{ marginBottom: 0 }}>
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
                  <Col span={12}>
                    <Form.Item label="Client ID" name="oauth2_client_id" rules={[{ required: true }]}>
                      <Input />
                    </Form.Item>
                  </Col>
                  <Col span={12}>
                    <Form.Item label="Client Secret" name="oauth2_client_secret" rules={[{ required: true }]}>
                      <Input.Password />
                    </Form.Item>
                  </Col>
                </Row>
                <Row gutter={16}>
                  <Col span={16}>
                    <Form.Item label="Token URL" name="oauth2_token_url" rules={[{ required: true }]}>
                      <Input placeholder="https://auth.example.com/oauth2/token" />
                    </Form.Item>
                  </Col>
                  <Col span={8}>
                    <Form.Item label="Scopes" name="oauth2_scopes" extra="逗号或空格分隔" style={{ marginBottom: 0 }}>
                      <Input placeholder="mcp.read mcp.write" />
                    </Form.Item>
                  </Col>
                </Row>
              </>
            )}
          </div>
        )}

        {/* 重连配置 */}
        <div style={sectionStyle}>
          <SectionHeader icon={<ReloadOutlined />} title="重连配置" subtitle="断连后自动重试策略" />
          <Form.Item label="启用自动重连" name="retry_enabled" valuePropName="checked" style={retryEnabled ? undefined : { marginBottom: 0 }}>
            <Switch />
          </Form.Item>

          {retryEnabled && (
            <>
              <div style={{ background: '#f6ffed', border: '1px solid #b7eb8f', borderRadius: 8, padding: '10px 14px', marginBottom: 16 }}>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  等待时间 = min(初始延迟 × 退避系数^n, 最大延迟)，指数退避直至超过最大重试次数
                </Text>
              </div>
              <Row gutter={16}>
                <Col span={6}>
                  <Form.Item label="最大重试次数" name="retry_max_retries" rules={[{ required: true }]}>
                    <InputNumber min={1} max={20} style={{ width: '100%' }} />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item label="初始延迟（ms）" name="retry_initial_delay_ms" rules={[{ required: true }]}>
                    <InputNumber min={100} max={60000} step={100} style={{ width: '100%' }} />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item label="最大延迟（ms）" name="retry_max_delay_ms" rules={[{ required: true }]}>
                    <InputNumber min={1000} max={300000} step={1000} style={{ width: '100%' }} />
                  </Form.Item>
                </Col>
                <Col span={6}>
                  <Form.Item label="退避系数" name="retry_backoff_factor" rules={[{ required: true }]} style={{ marginBottom: 0 }}>
                    <InputNumber min={1.0} max={10.0} step={0.5} style={{ width: '100%' }} />
                  </Form.Item>
                </Col>
              </Row>
            </>
          )}
        </div>

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/mcp')}>取消</Button>
          <Button type="primary" htmlType="submit" loading={submitting}>
            添加服务器
          </Button>
        </div>
      </Form>
    </div>
  );
}

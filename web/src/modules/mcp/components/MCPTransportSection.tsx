import { LinkOutlined } from '@ant-design/icons';
import { Form, Input } from 'antd';

import { mcpSectionStyle } from './mcpFormStyles';

import { SectionHeader } from '@/shared/ui';


const { TextArea } = Input;

export const MCPTransportSection = () => (
  <div style={mcpSectionStyle}>
    <SectionHeader
      icon={<LinkOutlined />}
      title="连接配置"
      subtitle="远程服务器地址与请求头"
    />
    <Form.Item
      label="服务器 URL"
      name="url"
      rules={[{ required: true, message: '请输入 URL' }]}
    >
      <Input placeholder="https://mcp.example.com/sse" />
    </Form.Item>
    <Form.Item
      label="自定义请求头"
      name="headers"
      extra="每行一个，格式：Header-Name: value"
      style={{ marginBottom: 0 }}
    >
      <TextArea
        rows={3}
        placeholder={'X-Custom-Header: value\nX-Request-Source: stratum'}
        style={{ fontFamily: 'monospace', fontSize: 13 }}
      />
    </Form.Item>
  </div>
);

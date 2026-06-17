import { LinkOutlined } from '@ant-design/icons';
import { Form, Input } from 'antd';

import { mcpSectionStyle } from './mcpFormStyles';

import { SectionHeader } from '@/shared/ui';


const { TextArea } = Input;

interface Props {
  transport?: string;
}

export const MCPTransportSection = ({ transport }: Props) => (
  <div style={mcpSectionStyle}>
    <SectionHeader
      icon={<LinkOutlined />}
      title="连接配置"
      subtitle={transport === 'stdio' ? '本地进程启动参数' : '远程服务器地址与请求头'}
    />
    {transport === 'stdio' ? (
      <>
        <Form.Item
          label="命令"
          name="command"
          rules={[{ required: true, message: '请输入启动命令' }]}
        >
          <Input placeholder="例如：npx 或 /usr/bin/python3" />
        </Form.Item>
        <Form.Item label="参数" name="args" extra="空格分隔，例如：-m mcp_server --port 8080">
          <Input placeholder="mcp-server-filesystem /home/user/projects" />
        </Form.Item>
        <Form.Item
          label="环境变量"
          name="env"
          extra="每行一个，格式：KEY=VALUE"
          style={{ marginBottom: 0 }}
        >
          <TextArea
            rows={4}
            placeholder={'ANTHROPIC_API_KEY=sk-xxx\nDATA_DIR=/tmp/data'}
            style={{ fontFamily: 'monospace', fontSize: 13 }}
          />
        </Form.Item>
      </>
    ) : (
      <>
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
      </>
    )}
  </div>
);

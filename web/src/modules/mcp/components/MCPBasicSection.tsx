import { ApiOutlined } from '@ant-design/icons';
import { Col, Form, Input, InputNumber, Row, Select, Typography } from 'antd';

import { mcpSectionStyle } from './mcpFormStyles';

import { MCP_MAX_TIMEOUT_SEC } from '@/constants';
import { SectionHeader } from '@/shared/ui';


const { Option } = Select;
const { Text } = Typography;

const TRANSPORT_DESCS: Record<string, string> = {
  stdio: '通过标准输入/输出通信，适合本地命令行工具',
  'streamable-http': '流式 HTTP，MCP 2025-03-26 规范推荐方式',
};

export const MCPBasicSection = () => (
  <div style={mcpSectionStyle}>
    <SectionHeader icon={<ApiOutlined />} title="基本信息" />
    <Row gutter={16}>
      <Col xs={24} md={14}>
        <Form.Item
          label="名称"
          name="name"
          rules={[{ required: true, message: '请输入服务器名称' }]}
        >
          <Input placeholder="例如：filesystem-server" maxLength={64} />
        </Form.Item>
      </Col>
      <Col xs={24} md={5}>
        <Form.Item label="版本" name="version">
          <Input placeholder="1.0.0" maxLength={32} />
        </Form.Item>
      </Col>
      <Col xs={24} md={5}>
        <Form.Item label="超时（秒）" name="timeout_sec" rules={[{ required: true }]}>
          <InputNumber min={1} max={MCP_MAX_TIMEOUT_SEC} style={{ width: '100%' }} />
        </Form.Item>
      </Col>
    </Row>
    <Form.Item
      label="传输协议"
      name="transport"
      rules={[{ required: true }]}
      style={{ marginBottom: 0 }}
    >
      <Select>
        {Object.entries(TRANSPORT_DESCS).map(([k, desc]) => (
          <Option key={k} value={k}>
            <Text strong style={{ marginRight: 6 }}>
              {k}
            </Text>
            <Text type="secondary" style={{ fontSize: 12 }}>
              — {desc}
            </Text>
          </Option>
        ))}
      </Select>
    </Form.Item>
  </div>
);

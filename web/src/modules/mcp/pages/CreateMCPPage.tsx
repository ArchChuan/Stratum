import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Typography } from 'antd';
import { useNavigate } from 'react-router-dom';

import { MCPAuthSection } from '../components/MCPAuthSection';
import { MCPBasicSection } from '../components/MCPBasicSection';
import { MCPRetrySection } from '../components/MCPRetrySection';
import { MCPTransportSection } from '../components/MCPTransportSection';
import { useCreateMCPPage } from '../hooks/useCreateMCPPage';

import {
  MCP_DEFAULT_TIMEOUT_SEC,
  MCP_RETRY_BACKOFF_FACTOR,
  MCP_RETRY_INITIAL_DELAY_MS,
  MCP_RETRY_MAX_DELAY_MS,
  MCP_RETRY_MAX_RETRIES,
} from '@/constants';

const { Title, Text } = Typography;

export const CreateMCPPage = () => {
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
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/mcp')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            添加 MCP 服务器
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            配置外部工具服务器连接
          </Text>
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
        <MCPBasicSection />
        <MCPTransportSection transport={transport} />
        {isHTTP && <MCPAuthSection authType={authType} />}
        <MCPRetrySection retryEnabled={retryEnabled} />

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/mcp')}>取消</Button>
          <Button type="primary" htmlType="submit" loading={submitting}>
            添加服务器
          </Button>
        </div>
      </Form>
    </div>
  );
};

export default CreateMCPPage;

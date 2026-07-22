import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Skeleton, Typography } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';

import { MCPAuthSection } from '../components/MCPAuthSection';
import { MCPBasicSection } from '../components/MCPBasicSection';
import { MCPRetrySection } from '../components/MCPRetrySection';
import { MCPTransportSection } from '../components/MCPTransportSection';
import { useEditMCPPage } from '../hooks/useEditMCPPage';

const { Title, Text } = Typography;

export const EditMCPPage = () => {
  const { id } = useParams<{ id: string }>();
  const [form] = Form.useForm();
  const { loading, submitting, initialValues, handleFinish } = useEditMCPPage(id!);
  const navigate = useNavigate();

  const transport = Form.useWatch('transport', form);
  const authType = Form.useWatch('auth_type', form);
  const retryEnabled = Form.useWatch('retry_enabled', form);
  const isHTTP = transport && transport !== 'stdio';

  if (loading) return <Skeleton active />;

  return (
    <div className="responsive-form-page">
      <div className="responsive-detail-header" style={{ marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/mcp')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            编辑 MCP 服务器
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            修改配置后将自动断开并重新连接
          </Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={initialValues ?? undefined}
      >
        <MCPBasicSection />
        <MCPTransportSection transport={transport} />
        {isHTTP && (
          <MCPAuthSection
            authType={authType}
            editing
            credentialConfigured={initialValues?.credential_configured ?? false}
          />
        )}
        <MCPRetrySection retryEnabled={retryEnabled} />

        <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/mcp')}>取消</Button>
          <Button type="primary" htmlType="submit" loading={submitting}>
            保存并重连
          </Button>
        </div>
      </Form>
    </div>
  );
};

export default EditMCPPage;

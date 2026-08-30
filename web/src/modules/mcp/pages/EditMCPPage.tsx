import { ArrowLeftOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Skeleton, Typography } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';

import { MCPAuthSection } from '../components/MCPAuthSection';
import { MCPBasicSection } from '../components/MCPBasicSection';
import { MCPRetrySection } from '../components/MCPRetrySection';
import { MCPTransportSection } from '../components/MCPTransportSection';
import { useEditMCPPage } from '../hooks/useEditMCPPage';

import { RequestEditorButton } from '@/shared/components';

const { Title, Text } = Typography;

export const EditMCPPage = () => {
  const { id } = useParams<{ id: string }>();
  const [form] = Form.useForm();
  const { loading, submitting, initialValues, handleFinish, canEdit } = useEditMCPPage(id!);
  const navigate = useNavigate();
  const readOnly = !canEdit;

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
            {readOnly ? '查看 MCP 服务器配置' : '编辑 MCP 服务器'}
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            {readOnly ? '只读查看，如需修改请申请编辑权限' : '修改配置后将自动断开并重新连接'}
          </Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        disabled={readOnly}
        initialValues={initialValues ?? undefined}
      >
        {transport === 'stdio' && (
          <Alert
            type="warning"
            showIcon
            message="传输方式已停用，请改用 streamable-http"
            style={{ marginBottom: 16 }}
          />
        )}
        <MCPBasicSection />
        <MCPTransportSection />
        {isHTTP && (
          <MCPAuthSection
            authType={authType}
            editing
            credentialConfigured={initialValues?.credential_configured ?? false}
          />
        )}
        <MCPRetrySection retryEnabled={retryEnabled} />

        {!readOnly && (
          <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Button onClick={() => navigate('/mcp')}>取消</Button>
            <Button type="primary" htmlType="submit" loading={submitting}>
              保存并重连
            </Button>
          </div>
        )}
      </Form>
      {/* 申请编辑权限按钮必须放在 Form 外：<Form disabled={readOnly}> 通过 DisabledContext
          禁用表单内所有 antd 组件（含 Button），member 只读时须可点申请。 */}
      {readOnly && (
        <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <RequestEditorButton resourceType="mcp" resourceId={id ?? ''} />
        </div>
      )}
    </div>
  );
};

export default EditMCPPage;

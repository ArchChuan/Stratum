import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Typography } from 'antd';

import { AgentFormSections } from '../components/AgentFormSections';
import { useCreateAgentPage } from '../hooks/useCreateAgentPage';

const { Title, Text } = Typography;

export const CreateAgentPage = () => {
  const { form, loading, skills, mcpTools, workspaces, navigate, onFinish } =
    useCreateAgentPage();

  return (
    <div className="responsive-form-page">
      <div className="responsive-detail-header" style={{ marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/agents')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            创建 Agent
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            配置一个新的智能 Agent
          </Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
        initialValues={{
          type: 'react',
          maxIterations: 25,
          maxContextTokens: 8000,
          allowedSkills: [],
          memoryScope: 'user',
        }}
      >
        <AgentFormSections skills={skills} mcpTools={mcpTools} workspaces={workspaces} />

        <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/agents')}>取消</Button>
          <Button type="primary" htmlType="submit" loading={loading}>
            创建 Agent
          </Button>
        </div>
      </Form>
    </div>
  );
};

import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Typography } from 'antd';

import { AgentFormSections } from '../components/AgentFormSections';
import { useCreateAgentPage } from '../hooks/useCreateAgentPage';

const { Title, Text } = Typography;

export const CreateAgentPage = () => {
  const { form, loading, skills, mcpServers, workspaces, navigate, onFinish } =
    useCreateAgentPage();

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
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
          memoryEnabled: false,
          memoryScope: 'agent',
        }}
      >
        <AgentFormSections skills={skills} mcpServers={mcpServers} workspaces={workspaces} />

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/agents')}>取消</Button>
          <Button type="primary" htmlType="submit" loading={loading}>
            创建 Agent
          </Button>
        </div>
      </Form>
    </div>
  );
};

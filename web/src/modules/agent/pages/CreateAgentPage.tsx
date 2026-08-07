import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Typography } from 'antd';

import { AgentFormSections } from '../components/AgentFormSections';
import { useCreateAgentPage } from '../hooks/useCreateAgentPage';

import { AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';
import { useEditorCandidates } from '@/modules/iam';

const { Title, Text } = Typography;

export const CreateAgentPage = () => {
  const { form, loading, skills, mcpTools, workspaces, groupedModels, navigate, onFinish } =
    useCreateAgentPage();
  const { candidates, loading: editorCandidatesLoading } = useEditorCandidates();

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
          maxIterations: AGENT_DEFAULT_MAX_ITERATIONS,
          maxContextTokens: 8000,
          allowedSkills: [],
          memoryScope: 'user',
        }}
      >
        <AgentFormSections
          skills={skills}
          mcpTools={mcpTools}
          workspaces={workspaces}
          groupedModels={groupedModels}
          showEditors
          editorCandidates={candidates}
          editorCandidatesLoading={editorCandidatesLoading}
        />

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

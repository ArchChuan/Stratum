import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Typography } from 'antd';

import { AgentFormSections } from '../components/AgentFormSections';
import { useCreateAgentPage } from '../hooks/useCreateAgentPage';

import { AGENT_DEFAULT_MAX_CONTEXT_TOKENS, AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';
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
          maxContextTokens: AGENT_DEFAULT_MAX_CONTEXT_TOKENS, // 0 = 自动按模型窗口解析
          allowedSkills: [],
          memoryScope: 'user',
          // 委托默认关闭（DB DEFAULT false）：委托是显式能力，新建 agent 默认不派发，
          // 管理员在表单显式开启。深度/默认步数留空 = 0=unset，后端回落全局默认。
          delegateEnabled: false,
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

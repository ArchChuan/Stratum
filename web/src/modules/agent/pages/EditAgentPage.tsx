import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Skeleton, Typography } from 'antd';

import { AgentFormSections } from '../components/AgentFormSections';
import { useEditAgentPage } from '../hooks/useEditAgentPage';

import { AGENT_DEFAULT_MAX_ITERATIONS } from '@/constants';

const { Title, Text } = Typography;

export const EditAgentPage = () => {
  const {
    agent, form, loading, pageLoading, skills, mcpTools, workspaces, groupedModels,
    navigate, managementPath, onFinish,
  } = useEditAgentPage();

  if (pageLoading) {
    return (
      <div className="responsive-form-page">
        <Skeleton active paragraph={{ rows: 1 }} style={{ marginBottom: 24 }} />
        <div
          style={{
            background: '#fff',
            borderRadius: 12,
            border: '1px solid #f0f0f0',
            padding: 24,
            marginBottom: 16,
          }}
        >
          <Skeleton active paragraph={{ rows: 3 }} />
        </div>
        <div
          style={{
            background: '#fff',
            borderRadius: 12,
            border: '1px solid #f0f0f0',
            padding: 24,
          }}
        >
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      </div>
    );
  }

  return (
    <div className="responsive-form-page">
      <div className="responsive-detail-header" style={{ marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate(managementPath)} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            {agent?.isSystem ? '平台助手设置' : '编辑 Agent'}
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            修改 Agent 配置
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
          currentModel={agent?.llmModel}
          isSystem={agent?.isSystem}
        />

        <div className="responsive-form-actions" style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate(managementPath)}>取消</Button>
          <Button type="primary" htmlType="submit" loading={loading}>
            保存修改
          </Button>
        </div>
      </Form>
    </div>
  );
};

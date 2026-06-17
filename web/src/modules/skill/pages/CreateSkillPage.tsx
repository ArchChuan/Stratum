import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Typography } from 'antd';


import { SKILL_TYPE_META, SkillFormSections } from '../components/SkillFormSections';
import { useCreateSkillPage } from '../hooks/useCreateSkillPage';

import {
  SKILL_DEFAULT_MAX_TOKENS,
  SKILL_DEFAULT_TEMPERATURE,
  SKILL_DEFAULT_TIMEOUT_SEC,
} from '@/constants';

const { Title, Text } = Typography;

export const CreateSkillPage = () => {
  const { form, loading, availableModels, modelsLoading, skillType, language, navigate, onFinish } =
    useCreateSkillPage();

  const selectedMeta = skillType ? SKILL_TYPE_META[skillType] : SKILL_TYPE_META.code;

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            创建技能
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            定义可供 Agent 调用的技能
          </Text>
        </div>
      </div>

      <Form
        form={form}
        layout="vertical"
        onFinish={onFinish}
        initialValues={{
          type: 'code',
          language: 'python',
          method: 'POST',
          timeoutSec: SKILL_DEFAULT_TIMEOUT_SEC,
          temperature: SKILL_DEFAULT_TEMPERATURE,
          maxTokens: SKILL_DEFAULT_MAX_TOKENS,
        }}
      >
        <SkillFormSections
          form={form}
          skillType={skillType}
          language={language}
          availableModels={availableModels}
          modelsLoading={modelsLoading}
        />

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={() => navigate('/skills')}>取消</Button>
          <Button
            type="primary"
            htmlType="submit"
            loading={loading}
            style={{ background: selectedMeta.color, borderColor: selectedMeta.color }}
          >
            创建技能
          </Button>
        </div>
      </Form>
    </div>
  );
};

export default CreateSkillPage;

import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Spin, Typography } from 'antd';


import { SKILL_TYPE_META, SkillFormSections } from '../components/SkillFormSections';
import { useEditSkillPage } from '../hooks/useEditSkillPage';

import {
  SKILL_DEFAULT_MAX_TOKENS,
  SKILL_DEFAULT_TEMPERATURE,
  SKILL_DEFAULT_TIMEOUT_SEC,
} from '@/constants';

const { Title, Text } = Typography;

export const EditSkillPage = () => {
  const {
    form,
    loading,
    fetchLoading,
    availableModels,
    modelsLoading,
    skillType,
    language,
    navigate,
    onFinish,
  } = useEditSkillPage();

  const selectedMeta = skillType ? SKILL_TYPE_META[skillType] : SKILL_TYPE_META.code;

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 24 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            编辑技能
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            修改技能配置
          </Text>
        </div>
      </div>

      <Spin spinning={fetchLoading}>
        <Form
          form={form}
          layout="vertical"
          onFinish={onFinish}
          initialValues={{
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
            isEdit
          />

          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Button onClick={() => navigate('/skills')}>取消</Button>
            <Button
              type="primary"
              htmlType="submit"
              loading={loading}
              style={{ background: selectedMeta.color, borderColor: selectedMeta.color }}
            >
              保存
            </Button>
          </div>
        </Form>
      </Spin>
    </div>
  );
};

export default EditSkillPage;

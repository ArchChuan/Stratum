import { ArrowLeftOutlined } from '@ant-design/icons';
import { Button, Form, Input, Spin, Typography } from 'antd';


import { SKILL_TYPE_META, SkillFormSections } from '../components/SkillFormSections';
import { useEditSkillPage } from '../hooks/useEditSkillPage';

import {
  SKILL_DEFAULT_MAX_TOKENS,
  SKILL_DEFAULT_TEMPERATURE,
  SKILL_DEFAULT_TIMEOUT_SEC,
} from '@/constants';

const { Title, Text } = Typography;
const { TextArea } = Input;

export const EditSkillPage = () => {
  const {
    form,
    loading,
    fetchLoading,
    availableModels,
    modelsLoading,
    skillType,
    language,
    testInput,
    testLoading,
    testResult,
    navigate,
    onFinish,
    setTestInput,
    onRunTest,
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

        <div style={{ marginTop: 32, paddingTop: 24, borderTop: '1px solid #f0f0f0' }}>
          <Title level={5} style={{ marginTop: 0 }}>
            测试运行
          </Title>
          <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
            输入一段文本或 JSON，直接验证这个技能的输出
          </Text>
          <TextArea
            rows={5}
            value={testInput}
            onChange={(event) => setTestInput(event.target.value)}
            placeholder='例如：{"text":"客户反馈快递三天没有更新"}'
          />
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 12 }}>
            <Button type="primary" loading={testLoading} onClick={onRunTest}>
              运行测试
            </Button>
          </div>
          {testResult ? (
            <div style={{ marginTop: 16 }}>
              <Text type="secondary">
                Trace：{testResult.traceID || '无'} · 耗时：{testResult.durationMs ?? 0} ms
              </Text>
              <pre
                style={{
                  marginTop: 8,
                  padding: 12,
                  background: '#f7f8fa',
                  borderRadius: 6,
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                }}
              >
                {typeof testResult.result === 'string'
                  ? testResult.result
                  : JSON.stringify(testResult.result, null, 2)}
              </pre>
            </div>
          ) : null}
        </div>
      </Spin>
    </div>
  );
};

export default EditSkillPage;

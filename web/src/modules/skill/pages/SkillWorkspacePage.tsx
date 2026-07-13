import { ArrowLeftOutlined, PlayCircleOutlined, SendOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, Select, Skeleton, Space, Switch, Tabs, Typography, message } from 'antd';
import type { ReactNode } from 'react';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import { parseSkillTestInput, type SkillFormValues, type SkillVersion, type SkillWorkspace } from '../model/skill';

import { extractErrorMessage } from '@/shared/lib';

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

interface CapabilityFormValues {
  goal: string;
  whenToUse: string;
  inputSpec?: string;
  outputSpec?: string;
}

interface ContractFormValues {
  toolName: string;
  description: string;
  callingGuidance?: string;
  confirmed: boolean;
  inputSchemaJson: string;
  outputSchemaJson: string;
}

interface ImplementationFormValues {
  mode: string;
  sourceJson: string;
  runtimeJson?: string;
  permissionsJson?: string;
  secretRefs?: string;
}

export const SkillWorkspacePage = () => {
  const { id = '' } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [workspace, setWorkspace] = useState<SkillWorkspace | null>(null);
  const [loading, setLoading] = useState(true);
  const [capabilityLoading, setCapabilityLoading] = useState(false);
  const [contractLoading, setContractLoading] = useState(false);
  const [implementationLoading, setImplementationLoading] = useState(false);
  const [testLoading, setTestLoading] = useState(false);
  const [publishLoading, setPublishLoading] = useState(false);
  const [testInput, setTestInput] = useState('{"input":"客户反馈快递三天没有更新"}');
  const [testResult, setTestResult] = useState<unknown>(null);
  const [error, setError] = useState('');
  const [capabilityForm] = Form.useForm<CapabilityFormValues>();
  const [contractForm] = Form.useForm<ContractFormValues>();
  const [implementationForm] = Form.useForm<ImplementationFormValues>();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const data = await skillApi.getWorkspace(id);
        if (!cancelled) {
          setWorkspace(data);
          fillForms(data.draft, capabilityForm, contractForm, implementationForm);
        }
      } catch (err) {
        if (!cancelled) setError(extractErrorMessage(err) || '加载技能工作台失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, capabilityForm, contractForm, implementationForm]);

  if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
  if (error) return <Alert type="error" message={error} showIcon />;
  if (!workspace) return <Alert type="warning" message="技能工作台不存在" showIcon />;

  const { skill, draft } = workspace;

  const updateDraft = (updated: SkillVersion) => {
    const next = { ...workspace, draft: updated };
    setWorkspace(next);
    fillForms(updated, capabilityForm, contractForm, implementationForm);
  };

  const handleSaveCapability = async (values: CapabilityFormValues) => {
    setCapabilityLoading(true);
    try {
      updateDraft(await skillApi.updateCapability(skill.id, values));
      message.success({ content: '能力定义已保存', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '保存能力定义失败', duration: 0 });
    } finally {
      setCapabilityLoading(false);
    }
  };

  const handleSaveContract = async (values: ContractFormValues) => {
    setContractLoading(true);
    try {
      updateDraft(
        await skillApi.updateContract(skill.id, {
          toolName: values.toolName,
          description: values.description,
          callingGuidance: values.callingGuidance,
          inputSchema: parseJsonObject(values.inputSchemaJson, '输入 Schema'),
          outputSchema: parseJsonObject(values.outputSchemaJson, '输出 Schema'),
          confirmed: values.confirmed,
        }),
      );
      message.success({ content: '工具契约已保存', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '保存工具契约失败', duration: 0 });
    } finally {
      setContractLoading(false);
    }
  };

  const handleSaveImplementation = async (values: ImplementationFormValues) => {
    setImplementationLoading(true);
    try {
      updateDraft(
        await skillApi.updateImplementation(skill.id, {
          mode: values.mode,
          source: parseJsonObject(values.sourceJson, '实现内容'),
          runtime: parseOptionalJsonObject(values.runtimeJson, '运行参数'),
          permissions: parseOptionalJsonObject(values.permissionsJson, '权限声明'),
          secretRefs: parseSecretRefs(values.secretRefs),
        }),
      );
      message.success({ content: '实现配置已保存', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '保存实现配置失败', duration: 0 });
    } finally {
      setImplementationLoading(false);
    }
  };

  const handleRunDraftTest = async () => {
    setTestLoading(true);
    setTestResult(null);
    try {
      const result = await skillApi.testDraft({
        skill: buildLegacyDraftSkill(skill.name, draft),
        input: parseSkillTestInput(testInput),
      });
      setTestResult(result.result ?? result);
      message.success({ content: '草稿测试已完成', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '草稿测试失败', duration: 0 });
    } finally {
      setTestLoading(false);
    }
  };

  const handlePublish = async () => {
    setPublishLoading(true);
    try {
      const published = await skillApi.publish(skill.id);
      setWorkspace({
        ...workspace,
        skill: {
          ...skill,
          status: 'published',
          activeVersionId: published.id,
          draftVersionId: skill.draftVersionId,
        },
        draft: published,
      });
      message.success({ content: '技能已发布为可调用工具', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '发布失败，请检查契约确认和测试样例', duration: 0 });
    } finally {
      setPublishLoading(false);
    }
  };

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 20 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')} type="text">
          返回
        </Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>
            {skill.name}
          </Title>
          <Text type="secondary">
            状态：{skill.status} · 草稿：{skill.draftVersionId || '无'} · 当前版本：{skill.activeVersionId || '未发布'}
          </Text>
        </div>
      </div>

      <Tabs
        items={[
          {
            key: 'capability',
            label: '能力',
            children: (
              <Form form={capabilityForm} layout="vertical" onFinish={handleSaveCapability}>
                <Form.Item label="能力目标" name="goal" rules={[{ required: true, message: '请输入能力目标' }]}>
                  <TextArea rows={3} />
                </Form.Item>
                <Form.Item label="调用时机" name="whenToUse" rules={[{ required: true, message: '请输入调用时机' }]}>
                  <TextArea rows={3} />
                </Form.Item>
                <Form.Item label="输入说明" name="inputSpec">
                  <TextArea rows={2} placeholder="由样例推断，可在这里补充结构说明" />
                </Form.Item>
                <Form.Item label="输出说明" name="outputSpec">
                  <TextArea rows={2} placeholder="由样例推断，可在这里补充结构说明" />
                </Form.Item>
                <ActionRow>
                  <Button type="primary" htmlType="submit" loading={capabilityLoading}>
                    保存能力
                  </Button>
                </ActionRow>
              </Form>
            ),
          },
          {
            key: 'contract',
            label: '契约',
            children: (
              <Form form={contractForm} layout="vertical" onFinish={handleSaveContract}>
                <Form.Item label="工具名" name="toolName" rules={[{ required: true, message: '请输入工具名' }]}>
                  <Input placeholder="classify_complaint" />
                </Form.Item>
                <Form.Item label="工具说明" name="description" rules={[{ required: true, message: '请输入工具说明' }]}>
                  <TextArea rows={2} />
                </Form.Item>
                <Form.Item label="调用指引" name="callingGuidance">
                  <TextArea rows={2} placeholder="告诉 Agent 什么时候应该调用这个工具" />
                </Form.Item>
                <Form.Item label="输入 Schema" name="inputSchemaJson" rules={[{ required: true, message: '请输入输入 Schema' }]}>
                  <TextArea rows={8} />
                </Form.Item>
                <Form.Item label="输出 Schema" name="outputSchemaJson" rules={[{ required: true, message: '请输入输出 Schema' }]}>
                  <TextArea rows={8} />
                </Form.Item>
                <Form.Item label="确认契约" name="confirmed" valuePropName="checked" extra="发布前必须确认，确认后 Agent 才能把它当成稳定工具协议。">
                  <Switch checkedChildren="已确认" unCheckedChildren="未确认" />
                </Form.Item>
                <ActionRow>
                  <Button type="primary" htmlType="submit" loading={contractLoading}>
                    保存契约
                  </Button>
                </ActionRow>
              </Form>
            ),
          },
          {
            key: 'implementation',
            label: '实现',
            children: (
              <Form form={implementationForm} layout="vertical" onFinish={handleSaveImplementation}>
                <Form.Item label="实现方式" name="mode" rules={[{ required: true, message: '请选择实现方式' }]}>
                  <Select
                    options={[
                      { value: 'prompt', label: 'Prompt' },
                      { value: 'llm', label: 'LLM' },
                      { value: 'code', label: 'Code' },
                      { value: 'http', label: 'HTTP' },
                    ]}
                  />
                </Form.Item>
                <Form.Item label="实现内容 JSON" name="sourceJson" rules={[{ required: true, message: '请输入实现内容' }]}>
                  <TextArea rows={10} />
                </Form.Item>
                <Form.Item label="运行参数 JSON" name="runtimeJson">
                  <TextArea rows={5} placeholder='{"model":"gpt-4.1-mini","temperature":0.2}' />
                </Form.Item>
                <Form.Item label="权限声明 JSON" name="permissionsJson">
                  <TextArea rows={4} placeholder='{"network":false}' />
                </Form.Item>
                <Form.Item label="密钥引用" name="secretRefs" extra="一行一个引用名，只保存引用，不保存密钥值。">
                  <TextArea rows={3} />
                </Form.Item>
                <ActionRow>
                  <Button type="primary" htmlType="submit" loading={implementationLoading}>
                    保存实现
                  </Button>
                </ActionRow>
              </Form>
            ),
          },
          {
            key: 'tests',
            label: '测试',
            children: (
              <Space direction="vertical" size={16} style={{ width: '100%' }}>
                <Alert type="info" showIcon message="这里运行的是当前草稿实现，不需要先挂到 Agent。" />
                <TextArea rows={6} value={testInput} onChange={(event) => setTestInput(event.target.value)} />
                <ActionRow>
                  <Button icon={<PlayCircleOutlined />} type="primary" loading={testLoading} onClick={handleRunDraftTest}>
                    运行草稿测试
                  </Button>
                </ActionRow>
                {testResult !== null ? (
                  <pre style={{ margin: 0, padding: 12, background: '#f6f8fa', overflow: 'auto' }}>
                    {JSON.stringify(testResult, null, 2)}
                  </pre>
                ) : null}
              </Space>
            ),
          },
          {
            key: 'versions',
            label: '版本',
            children: (
              <Space direction="vertical" size={16} style={{ width: '100%' }}>
                <Alert
                  type={draft.status === 'published' ? 'success' : 'warning'}
                  showIcon
                  message={draft.status === 'published' ? `已发布版本 v${draft.versionNo || 1}` : '当前仍是草稿，发布后 Agent 才能稳定调用。'}
                />
                <Paragraph style={{ marginBottom: 0 }}>
                  版本 ID：{draft.id}
                  <br />
                  工具名：{String(draft.toolContract.toolName || '')}
                </Paragraph>
                <ActionRow>
                  <Button icon={<SendOutlined />} type="primary" loading={publishLoading} onClick={handlePublish}>
                    发布当前草稿
                  </Button>
                </ActionRow>
              </Space>
            ),
          },
        ]}
      />
    </div>
  );
};

const ActionRow = ({ children }: { children: ReactNode }) => (
  <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>{children}</div>
);

const fillForms = (
  draft: SkillVersion,
  capabilityForm: ReturnType<typeof Form.useForm<CapabilityFormValues>>[0],
  contractForm: ReturnType<typeof Form.useForm<ContractFormValues>>[0],
  implementationForm: ReturnType<typeof Form.useForm<ImplementationFormValues>>[0],
) => {
  capabilityForm.setFieldsValue({
    goal: String(draft.capability.goal || ''),
    whenToUse: String(draft.capability.whenToUse || ''),
    inputSpec: String(draft.capability.inputSpec || ''),
    outputSpec: String(draft.capability.outputSpec || ''),
  });
  contractForm.setFieldsValue({
    toolName: String(draft.toolContract.toolName || ''),
    description: String(draft.toolContract.description || ''),
    callingGuidance: String(draft.toolContract.callingGuidance || ''),
    confirmed: Boolean(draft.toolContract.confirmed),
    inputSchemaJson: stringifyJson(draft.toolContract.inputSchema || { type: 'object' }),
    outputSchemaJson: stringifyJson(draft.toolContract.outputSchema || { type: 'object' }),
  });
  implementationForm.setFieldsValue({
    mode: String(draft.implementation.mode || 'prompt'),
    sourceJson: stringifyJson(draft.implementation.source || { promptTemplate: '请处理输入：{{.input}}' }),
    runtimeJson: stringifyJson(draft.implementation.runtime || {}),
    permissionsJson: stringifyJson(draft.implementation.permissions || {}),
    secretRefs: Array.isArray(draft.implementation.secretRefs) ? draft.implementation.secretRefs.join('\n') : '',
  });
};

const stringifyJson = (value: unknown) => JSON.stringify(value || {}, null, 2);

const parseJsonObject = (raw: string | undefined, label: string): Record<string, unknown> => {
  try {
    const parsed = JSON.parse((raw || '{}').trim() || '{}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error(`${label} 必须是 JSON 对象`);
    }
    return parsed as Record<string, unknown>;
  } catch (err) {
    if (err instanceof Error && err.message.includes('必须是 JSON 对象')) throw err;
    throw new Error(`${label} 不是合法 JSON`);
  }
};

const parseOptionalJsonObject = (raw: string | undefined, label: string): Record<string, unknown> | undefined => {
  if (!raw || !raw.trim()) return undefined;
  const parsed = parseJsonObject(raw, label);
  return Object.keys(parsed).length > 0 ? parsed : undefined;
};

const parseSecretRefs = (raw: string | undefined) =>
  (raw || '')
    .split('\n')
    .map((item) => item.trim())
    .filter(Boolean);

const buildLegacyDraftSkill = (name: string, draft: SkillVersion): SkillFormValues => {
  const source = asRecord(draft.implementation.source);
  const runtime = asRecord(draft.implementation.runtime);
  const mode = String(draft.implementation.mode || 'prompt') as SkillFormValues['type'];
  return {
    name,
    description: String(draft.capability.goal || draft.toolContract.description || ''),
    type: mode,
    promptTemplate: String(source.promptTemplate || '请处理输入：{{.input}}'),
    code: typeof source.code === 'string' ? source.code : undefined,
    language: typeof source.language === 'string' ? source.language : undefined,
    systemPrompt: typeof source.systemPrompt === 'string' ? source.systemPrompt : undefined,
    model: typeof runtime.model === 'string' ? runtime.model : undefined,
    temperature: typeof runtime.temperature === 'number' ? runtime.temperature : undefined,
    maxTokens: typeof runtime.maxTokens === 'number' ? runtime.maxTokens : undefined,
    url: typeof source.url === 'string' ? source.url : undefined,
    method: typeof source.method === 'string' ? source.method : undefined,
    headers: isStringRecord(source.headers) ? source.headers : undefined,
    bodyTemplate: typeof source.bodyTemplate === 'string' ? source.bodyTemplate : undefined,
    timeoutSec: typeof runtime.timeoutSec === 'number' ? runtime.timeoutSec : undefined,
  };
};

const asRecord = (value: unknown): Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value as Record<string, unknown>;
};

const isStringRecord = (value: unknown): value is Record<string, string> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  return Object.values(value).every((item) => typeof item === 'string');
};

export default SkillWorkspacePage;

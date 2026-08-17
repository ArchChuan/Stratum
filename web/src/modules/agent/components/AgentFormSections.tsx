import { RobotOutlined, SettingOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Collapse, Form, Input, InputNumber, Select, Slider, Tag, Typography } from 'antd';
import { useMemo } from 'react';

import type { GroupedModelOption } from '../model/agent';

import { AgentMemoryConfig } from './AgentMemoryConfig';

import {
  AGENT_CONTEXT_WINDOW_RATIO,
  AGENT_DEFAULT_MAX_OUTPUT_TOKENS,
  AGENT_DEFAULT_TEMPERATURE,
  AGENT_MAX_CONTEXT_TOKENS_MAX,
  AGENT_MAX_CONTEXT_TOKENS_MIN,
  AGENT_MAX_CONTEXT_TOKENS_STEP,
  AGENT_MAX_MAX_ITERATIONS,
  AGENT_MAX_TOKENS_MAX,
  AGENT_MAX_TOKENS_MIN,
  AGENT_MAX_TOKENS_STEP,
  AGENT_MIN_MAX_ITERATIONS,
  AGENT_TEMPERATURE_MAX,
  AGENT_TEMPERATURE_MIN,
  AGENT_TEMPERATURE_STEP,
  COMPACTION_DEFAULT_TEMPERATURE,
  COMPACTION_RECENT_GROUPS_DEFAULT,
  COMPACTION_SAFETY_RATIO_DEFAULT,
  COMPACTION_TEMP_MAX,
  COMPACTION_TEMP_MIN,
  REASONING_EFFORT_OPTIONS,
} from '@/constants';
import type { Member } from '@/modules/iam';
import type { Workspace } from '@/modules/knowledge';
import type { MCPToolOption } from '@/modules/mcp';
import { PromptDefaultViewer } from '@/modules/parameters/components/PromptDefaultViewer';
import type { Skill } from '@/modules/skill';
import { DefaultHint, SectionHeader } from '@/shared/ui';

const { Text } = Typography;
const { TextArea } = Input;
const { Option, OptGroup } = Select;

interface AgentFormSectionsProps {
  skills: Skill[];
  mcpTools: MCPToolOption[];
  workspaces: Workspace[];
  groupedModels: GroupedModelOption[];
  currentModel?: string;
  isSystem?: boolean;
  // 创建路径才展示可编辑人多选：普通资源更新请求体不带 editors。
  showEditors?: boolean;
  editorCandidates?: Member[];
  editorCandidatesLoading?: boolean;
}

export const AgentFormSections = ({
  skills,
  mcpTools,
  workspaces,
  groupedModels,
  currentModel,
  isSystem = false,
  showEditors = false,
  editorCandidates = [],
  editorCandidatesLoading = false,
}: AgentFormSectionsProps) => {
  const form = Form.useFormInstance();
  const selectedModel = Form.useWatch('llmModel', form);
  // 当前选中模型是否支持推理；未选中或模型不在托管目录（不可用/退役）时视为
  // 非推理，隐藏思考强度控件（fail-closed，与网关 unknown→清空+WARN 一致）。
  const supportsReasoning = useMemo(() => {
    if (!selectedModel) return false;
    return groupedModels.some((g) => g.models.some((m) => m.value === selectedModel && m.reasoning));
  }, [selectedModel, groupedModels]);
  // 当前选中模型的上下文窗口；模型不在托管目录（不可用/退役）时窗口未知。
  const selectedWindow = useMemo(() => {
    if (!selectedModel) return undefined;
    return groupedModels.flatMap((g) => g.models).find((m) => m.value === selectedModel)?.contextWindow;
  }, [selectedModel, groupedModels]);
  // 0.85×窗口 的推荐上下文预算（与后端 pkg/constants DefaultContextWindowRatio 同源）。
  const recommendedContextTokens = selectedWindow ? Math.round(selectedWindow * AGENT_CONTEXT_WINDOW_RATIO) : undefined;
  // 当前选中模型的最大输出（vendor maxOut）；模型不在托管目录（不可用/退役）时未知，
  // 此时 max_tokens=0 回落 pkg/constants.DefaultOutputReserveTokens。
  const selectedMaxTokens = useMemo(() => {
    if (!selectedModel) return undefined;
    return groupedModels.flatMap((g) => g.models).find((m) => m.value === selectedModel)?.maxTokens;
  }, [selectedModel, groupedModels]);
  // 0 = 未设置（使用平台默认）的字段：watch 值派生 unset，仅在未设置时渲染
  // DefaultHint（提示展示不写回，0=unset 语义不被破坏）。
  const temperature = Form.useWatch('temperature', form);
  const compactionSafetyRatio = Form.useWatch('compaction_safety_ratio', form);
  const compactionTemperature = Form.useWatch('compaction_temperature', form);
  const temperatureUnset = temperature == null || temperature === 0;
  const safetyRatioUnset = compactionSafetyRatio == null || compactionSafetyRatio === 0;
  const compactionTemperatureUnset = compactionTemperature == null || compactionTemperature === 0;

  // 仅用户 change 时联动：当前值为自动（null/undefined/0）且窗口已知时填入推荐值；
  // 显式值保留，清空/置 0 后再次选模型才回填，不破坏「0 = 自动」语义。
  // 注意：onChange 触发时 Form store 尚未更新，Form.useWatch 返回旧值，必须用
  // onChange 的 value 参数反查窗口，避免 stale closure 导致联动永远不触发。
  const handleModelChange = (value: string) => {
    const current = form.getFieldValue('maxContextTokens');
    const isAuto = current === null || current === undefined || current === 0;
    if (!isAuto) return;
    const window = groupedModels.flatMap((g) => g.models).find((m) => m.value === value)?.contextWindow;
    if (!window) return;
    form.setFieldValue('maxContextTokens', Math.round(window * AGENT_CONTEXT_WINDOW_RATIO));
  };

  return (
    <>
    <Form.Item name="type" hidden>
      <Input />
    </Form.Item>

    <div
      className="responsive-form-section"
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        marginBottom: 16,
      }}
    >
      <SectionHeader
        icon={<RobotOutlined />}
        title="基本信息"
        subtitle="Agent 的名称和对外描述"
      />
      <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入 Agent 名称' }]}>
        <Input placeholder="例如：数据分析助手" />
      </Form.Item>
      <Form.Item label="描述" name="description" style={{ marginBottom: 0 }}>
        <TextArea rows={2} placeholder="简述 Agent 的用途" />
      </Form.Item>
    </div>

    <div
      className="responsive-form-section"
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        marginBottom: 16,
      }}
    >
      <SectionHeader
        icon={<ThunderboltOutlined />}
        title="提示词"
        subtitle="定义 Agent 的角色和行为"
      />
      <Form.Item
        label="系统提示词"
        name="systemPrompt"
        style={{ marginBottom: 0 }}
        extra="在这里定义角色、能力范围、行为准则和响应格式"
      >
        <TextArea
          rows={8}
          placeholder={
            '你是一个专业的数据分析师，擅长从数据中提取洞察。\n\n行为准则：\n- 回答基于事实，不做无依据推断\n- 复杂问题先拆解再逐步回答\n\n响应格式：使用 Markdown 格式。'
          }
        />
      </Form.Item>
    </div>

    <div
      className="responsive-form-section"
      style={{
        background: '#fff',
        borderRadius: 12,
        border: '1px solid #f0f0f0',
        padding: 24,
        marginBottom: 16,
      }}
    >
      <SectionHeader
        icon={<SettingOutlined />}
        title="模型与工具"
        subtitle="选择模型并挂载工具和知识"
      />
      <Form.Item label="LLM 模型" name="llmModel" rules={[{ required: true, message: '请选择模型' }]}>
        <Select
          placeholder="选择推理模型"
          notFoundContent="模型管理中没有可用的推理模型"
          showSearch
          optionFilterProp="children"
          onChange={handleModelChange}
        >
          {currentModel &&
            !groupedModels.some((g) => g.models.some((m) => m.value === currentModel)) && (
              <Option value={currentModel} disabled>
                {currentModel}（当前不可用）
              </Option>
            )}
          {groupedModels.map((group) => (
            <OptGroup key={group.provider} label={group.provider}>
              {group.models.map((m) => (
                <Option key={m.value} value={m.value}>
                  {m.label}
                </Option>
              ))}
            </OptGroup>
          ))}
        </Select>
      </Form.Item>
      <Form.Item
        label="技能"
        name="allowedSkills"
        style={{ marginBottom: 16 }}
        extra={isSystem ? '系统助手的技能由平台统一管理，不可修改' : '激活后向 Agent 注入版本化指令，并按要求收窄 MCP、知识和记忆权限'}
      >
        <Select mode="multiple" placeholder="选择 Agent 可调用的技能" disabled={isSystem}>
          {skills.map((s) => (
            <Option key={s.id} value={s.id}>
              <Tag style={{ margin: '0 6px 0 0', border: 'none', fontSize: 11 }} color={s.status === 'published' ? 'green' : 'default'}>
                {s.status}
              </Tag>
              {s.name}
            </Option>
          ))}
        </Select>
      </Form.Item>
      <Form.Item
        label="MCP 工具"
        name="mcpToolIds"
        style={{ marginBottom: 16 }}
        extra={isSystem ? '系统助手的 MCP 工具由平台统一管理，不可修改' : '符合 Model Context Protocol 协议的结构化工具'}
      >
        <Select mode="multiple" placeholder="选择允许调用的 MCP 工具" disabled={isSystem}>
          {mcpTools.map((tool) => (
            <Option key={tool.id} value={tool.id}>
              {tool.label}
            </Option>
          ))}
        </Select>
      </Form.Item>
      <Form.Item label="知识库" name="knowledgeWorkspaceIds" extra={isSystem ? '系统助手的知识库由平台统一管理，不可修改' : '执行时自动检索相关文档'}>
        <Select mode="multiple" placeholder="选择知识库" disabled={isSystem}>
          {workspaces.map((w) => (
            <Option key={w.id || w.name} value={w.id || w.name}>
              {w.name}
            </Option>
          ))}
        </Select>
      </Form.Item>
      {showEditors && (
        <Form.Item
          label="可编辑人"
          name="editors"
          extra={isSystem ? '系统助手的可编辑人由平台统一管理，不可修改' : '可编辑人（租户管理员）可以修改此 Agent；删除仍仅限创建者或超级管理员'}
          style={{ marginBottom: 0 }}
        >
          <Select mode="multiple" placeholder="选择可编辑的管理员" allowClear disabled={isSystem} loading={editorCandidatesLoading}>
            {editorCandidates.map((member) => (
              <Option key={member.user_id} value={member.user_id}>
                {member.github_login || member.user_id}
              </Option>
            ))}
          </Select>
        </Form.Item>
      )}
      <Collapse
        ghost
        size="small"
        defaultActiveKey={['advanced']}
        items={[
          {
            key: 'advanced',
            label: (
              <Text type="secondary" style={{ fontSize: 13 }}>
                高级设置
              </Text>
            ),
            children: (
              <>
                <Form.Item
                  label="最大迭代次数"
                  name="maxIterations"
                  rules={[{ required: true, message: '请选择最大迭代次数' }]}
                  extra="限制 Agent 单次执行的最大推理和工具调用轮数"
                >
                  <Slider
                    min={AGENT_MIN_MAX_ITERATIONS}
                    max={AGENT_MAX_MAX_ITERATIONS}
                    marks={{ 1: '1', 30: '30', 60: '60', 90: '90' }}
                    ariaLabelForHandle="最大迭代次数"
                  />
                </Form.Item>
                <Form.Item
                  label="最大上下文 Token"
                  name="maxContextTokens"
                  rules={[{ required: true, message: '请输入最大上下文 Token' }, { type: 'number', min: AGENT_MAX_CONTEXT_TOKENS_MIN, message: '最小值为 0（0 = 自动按模型窗口解析）' }]}
                  extra={
                    selectedWindow
                      ? `推荐 ${recommendedContextTokens} tokens（模型窗口 ${selectedWindow} × ${Math.round(AGENT_CONTEXT_WINDOW_RATIO * 100)}%）；0 = 自动按模型窗口解析`
                      : '0 = 自动按模型窗口解析'
                  }
                >
                  <InputNumber min={AGENT_MAX_CONTEXT_TOKENS_MIN} max={AGENT_MAX_CONTEXT_TOKENS_MAX} step={AGENT_MAX_CONTEXT_TOKENS_STEP} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item
                  label="最大生成 Token（max_tokens）"
                  name="max_tokens"
                  extra={
                    selectedMaxTokens && selectedMaxTokens > 0
                      ? `推荐 ${selectedMaxTokens.toLocaleString()} tokens（模型最大输出）；0 = 不修改（保留现有值）`
                      : `平台兜底 ${AGENT_DEFAULT_MAX_OUTPUT_TOKENS.toLocaleString()} tokens（模型未知时）；0 = 不修改（保留现有值）`
                  }
                >
                  <InputNumber min={AGENT_MAX_TOKENS_MIN} max={AGENT_MAX_TOKENS_MAX} step={AGENT_MAX_TOKENS_STEP} style={{ width: '100%' }} />
                </Form.Item>
                {!isSystem && (
                  <>
                    <Form.Item
                      label="温度（temperature）"
                      name="temperature"
                      rules={[{
                        type: 'number',
                        min: AGENT_TEMPERATURE_MIN,
                        max: AGENT_TEMPERATURE_MAX,
                        message: `范围 ${AGENT_TEMPERATURE_MIN}~${AGENT_TEMPERATURE_MAX}（0 = 使用平台默认）`,
                      }]}
                      extra={
                        <>
                          控制输出的随机性与创造性：值越低，输出越确定、保守；值越高，输出越发散、多样。范围 0~1，0 = 未设置；
                          {temperatureUnset && <DefaultHint value={AGENT_DEFAULT_TEMPERATURE} />}
                        </>
                      }
                    >
                      <Slider
                        min={AGENT_TEMPERATURE_MIN}
                        max={AGENT_TEMPERATURE_MAX}
                        step={AGENT_TEMPERATURE_STEP}
                        marks={{ [AGENT_TEMPERATURE_MIN]: '0', [AGENT_TEMPERATURE_MAX]: '1' }}
                        ariaLabelForHandle="temperature"
                      />
                    </Form.Item>
                    {supportsReasoning && (
                      <Form.Item
                        label="思考强度（reasoning_effort）"
                        name="reasoning_effort"
                        extra="推理深度与 token 成本权衡：不设置 = 平台默认。仅对支持推理的模型生效，非推理模型由网关忽略"
                      >
                        <Select allowClear placeholder="平台默认">
                          {REASONING_EFFORT_OPTIONS.map((o) => (
                            <Option key={o.value} value={o.value}>
                              {o.label}
                            </Option>
                          ))}
                        </Select>
                      </Form.Item>
                    )}
                    <Form.Item
                      label="压缩最近轮数（compaction_recent_groups）"
                      name="compaction_recent_groups"
                      extra={`按轮次组压缩历史；0 = 自动推导（默认按上下文窗口推导，通常 ${COMPACTION_RECENT_GROUPS_DEFAULT} 组）`}
                    >
                      <Select allowClear placeholder="0（自动推导）">
                        <Option value={0}>0（自动推导）</Option>
                        <Option value={2}>2 组</Option>
                        <Option value={3}>3 组</Option>
                        <Option value={5}>5 组</Option>
                      </Select>
                    </Form.Item>
                    <Form.Item
                      label="压缩安全比例（compaction_safety_ratio）"
                      name="compaction_safety_ratio"
                      extra={
                        <>
                          压缩阈值：0 = 未设置（使用平台默认）；
                          {safetyRatioUnset && <DefaultHint value={COMPACTION_SAFETY_RATIO_DEFAULT} />}
                        </>
                      }
                    >
                      <Slider min={0} max={0.95} step={0.05} marks={{ 0: '0', 0.95: '0.95' }} ariaLabelForHandle="compaction_safety_ratio" />
                    </Form.Item>
                    <Form.Item
                      label="压缩提示词（compaction_prompt）"
                      name="compaction_prompt"
                      extra={
                        <>
                          压缩历史时的系统提示词；留空 = 使用内置默认压缩提示词
                          <PromptDefaultViewer promptKey="agent.compaction_prompt" />
                        </>
                      }
                    >
                      <TextArea rows={4} placeholder="留空使用内置默认压缩提示词" />
                    </Form.Item>
                    <Form.Item
                      label="压缩温度（compaction_temperature）"
                      name="compaction_temperature"
                      rules={[{
                        type: 'number',
                        min: COMPACTION_TEMP_MIN,
                        max: COMPACTION_TEMP_MAX,
                        message: `范围 ${COMPACTION_TEMP_MIN}~${COMPACTION_TEMP_MAX}（0 = 使用默认 ${COMPACTION_DEFAULT_TEMPERATURE}）`,
                      }]}
                      extra={
                        <>
                          压缩摘要的随机性：范围 0~1；0 = 未设置（使用默认 {COMPACTION_DEFAULT_TEMPERATURE}）；
                          {compactionTemperatureUnset && <DefaultHint value={COMPACTION_DEFAULT_TEMPERATURE} />}
                        </>
                      }
                    >
                      <Slider min={COMPACTION_TEMP_MIN} max={COMPACTION_TEMP_MAX} step={0.1} marks={{ 0: '0', 1: '1' }} ariaLabelForHandle="compaction_temperature" />
                    </Form.Item>
                    <Form.Item
                      label="压缩模型（compaction_model）"
                      name="compaction_model"
                      extra="执行历史压缩所用的模型；留空 = 跟随主 LLM 模型"
                      style={{ marginBottom: 0 }}
                    >
                      <Select allowClear placeholder="跟随主 LLM 模型" showSearch optionFilterProp="children">
                        {groupedModels.map((group) => (
                          <OptGroup key={group.provider} label={group.provider}>
                            {group.models.map((m) => (
                              <Option key={m.value} value={m.value}>
                                {m.label}
                              </Option>
                            ))}
                          </OptGroup>
                        ))}
                      </Select>
                    </Form.Item>
                  </>
                )}
              </>
            ),
          },
        ]}
      />
    </div>

    <AgentMemoryConfig groupedModels={groupedModels} />
  </>
  );
};

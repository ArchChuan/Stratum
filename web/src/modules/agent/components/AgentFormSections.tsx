import { BranchesOutlined, RobotOutlined, SettingOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Collapse, Form, Input, InputNumber, Select, Slider, Switch, Tag, Typography } from 'antd';
import { useMemo } from 'react';

import type { GroupedModelOption } from '../model/agent';


import {
  AGENT_CONTEXT_WINDOW_RATIO,
  AGENT_DELEGATE_DEFAULT_MAX_DEPTH,
  AGENT_DELEGATE_DEFAULT_MAX_STEPS,
  AGENT_DELEGATE_MAX_DEPTH,
  AGENT_DELEGATE_MAX_STEPS,
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
  MEMORY_SCOPE_OPTIONS,
  REASONING_EFFORT_OPTIONS,
} from '@/constants';
import type { Member } from '@/modules/iam';
import type { Workspace } from '@/modules/knowledge';
import { filterModelOption, ModelOptionLabel } from '@/modules/llm/components/ModelOptionLabel';
import type { MCPToolOption } from '@/modules/mcp';
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
  showEditors = false,
  editorCandidates = [],
  editorCandidatesLoading = false,
}: AgentFormSectionsProps) => {
  const form = Form.useFormInstance();
  const selectedModel = Form.useWatch('llmModel', form);
  // 委托开关关闭时禁用深度/步数输入（避免「改了不生效」的误导）。
  const delegateEnabled = Form.useWatch('delegateEnabled', form) ?? true;
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
  // 推理模型 max_tokens 语义 = thinking + answer，过低会截断思考（网关 floor 4096 兜底）。
  const maxTokensReasoningHint = supportsReasoning ? '；推理模型该值含思考长度，过低会截断思考' : '';
  // 0 = 未设置（使用平台默认）的字段：watch 值派生 unset，仅在未设置时渲染
  // DefaultHint（提示展示不写回，0=unset 语义不被破坏）。
  const temperature = Form.useWatch('temperature', form);
  const temperatureUnset = temperature == null || temperature === 0;

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
          filterOption={filterModelOption}
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
                <Option key={m.value} value={m.value} label={m.label}>
                  <ModelOptionLabel label={m.label} capabilities={m.capabilities} />
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
        extra="激活后向 Agent 注入版本化指令，并按要求收窄 MCP、知识和记忆权限"
      >
        <Select mode="multiple" placeholder="选择 Agent 可调用的技能">
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
        extra="符合 Model Context Protocol 协议的结构化工具"
      >
        <Select mode="multiple" placeholder="选择允许调用的 MCP 工具">
          {mcpTools.map((tool) => (
            <Option key={tool.id} value={tool.id}>
              {tool.label}
            </Option>
          ))}
        </Select>
      </Form.Item>
      <Form.Item label="知识库" name="knowledgeWorkspaceIds" extra="执行时自动检索相关文档">
        <Select mode="multiple" placeholder="选择知识库">
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
          extra="可编辑人（租户管理员）可以修改此 Agent；删除仍仅限创建者或超级管理员"
          style={{ marginBottom: 0 }}
        >
          <Select mode="multiple" placeholder="选择可编辑的管理员" allowClear loading={editorCandidatesLoading}>
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
                    (selectedMaxTokens && selectedMaxTokens > 0
                      ? `推荐 ${selectedMaxTokens.toLocaleString()} tokens（模型最大输出）`
                      : `平台兜底 ${AGENT_DEFAULT_MAX_OUTPUT_TOKENS.toLocaleString()} tokens（模型未知时）`) +
                    `；0 = 不修改（保留现有值）${maxTokensReasoningHint}`
                  }
                >
                  <InputNumber min={AGENT_MAX_TOKENS_MIN} max={AGENT_MAX_TOKENS_MAX} step={AGENT_MAX_TOKENS_STEP} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item
                  label="记忆范围"
                  name="memoryScope"
                  rules={[{ required: true, message: '请选择记忆范围' }]}
                  extra="用户级：记忆与当前用户绑定；Agent 级：该 Agent 的所有用户共享同一份记忆"
                >
                  <Select placeholder="选择记忆范围">
                    {MEMORY_SCOPE_OPTIONS.map((o) => (
                      <Option key={o.value} value={o.value}>
                        {o.label}
                      </Option>
                    ))}
                  </Select>
                </Form.Item>
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
              </>
            ),
          },
        ]}
      />
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
          icon={<BranchesOutlined />}
          title="子 Agent 委托"
          subtitle="开启后将边界清晰的子任务委托给隔离的子 Agent 执行"
        />
        <Form.Item
          label="启用子 Agent 委托"
          name="delegateEnabled"
          valuePropName="checked"
          extra="开启后该 Agent 可将子任务委托给隔离的子 Agent（复用当前完整配置）执行并回传摘要；只读子任务风险与父 Agent 相同"
        >
          <Switch />
        </Form.Item>
        <Form.Item
          label="最大委托深度"
          name="delegateMaxDepth"
          extra={`子 Agent 可继续嵌套派发的层数：1 = 仅主 Agent 直接派发一层（默认）；2 = 允许子 Agent 再派发一层；0 = 未设置（回落后端默认 ${AGENT_DELEGATE_DEFAULT_MAX_DEPTH}）`}
        >
          <InputNumber min={0} max={AGENT_DELEGATE_MAX_DEPTH} step={1} style={{ width: '100%' }} disabled={!delegateEnabled} />
        </Form.Item>
        <Form.Item
          label="委托默认最大推理步数"
          name="delegateDefaultMaxSteps"
          style={{ marginBottom: 0 }}
          extra={`子 Agent 单次委托的最大推理轮数上限；0 = 未设置（回落后端默认 ${AGENT_DELEGATE_DEFAULT_MAX_STEPS}），最高 ${AGENT_DELEGATE_MAX_STEPS}`}
        >
          <InputNumber min={0} max={AGENT_DELEGATE_MAX_STEPS} step={1} style={{ width: '100%' }} disabled={!delegateEnabled} />
        </Form.Item>
      </div>

  </>
  );
};

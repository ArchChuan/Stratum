import { DatabaseOutlined } from '@ant-design/icons';
import { Form, Input, Select, Slider } from 'antd';

import type { GroupedModelOption } from '../model/agent';

import {
  MEMORY_FACT_INJECTION_DEFAULT,
  MEMORY_FACT_INJECTION_MAX,
  MEMORY_FACT_INJECTION_MIN,
  MEMORY_HISTORY_INJECTION_DEFAULT,
  MEMORY_HISTORY_INJECTION_MAX,
  MEMORY_HISTORY_INJECTION_MIN,
  MEMORY_MAX_FACTS_DEFAULT,
  MEMORY_MAX_FACTS_MAX,
  MEMORY_MAX_FACTS_MIN,
  MEMORY_SCOPE_OPTIONS,
  RECALL_TOP_K_DEFAULT,
  RECALL_TOP_K_MAX,
  RECALL_TOP_K_MIN,
} from '@/constants';
import { SectionHeader } from '@/shared/ui';

const { TextArea } = Input;
const { Option, OptGroup } = Select;

interface AgentMemoryConfigProps {
  // 模型目录按 provider 分组，供提取模型选择器复用（同 AgentFormSections 主 LLM 模式）。
  groupedModels: GroupedModelOption[];
}

// 记忆参数。注入/提取/召回按 agent 生效：数值经 buildMemoryParameters 映射为
// agents.parameters JSONB 的 memory.* dotted 键，空值 = 不覆盖，回落代码默认。
// 提取模型选择器从模型管理目录选，存储值 = 模型名（后端校验目录存在性）。
// long_term_top_k 与 recall_top_k 语义重复（registry 标注 deprecated），不渲染。
export const AgentMemoryConfig = ({ groupedModels }: AgentMemoryConfigProps) => (
  <div
    style={{
      background: '#fff',
      borderRadius: 12,
      border: '1px solid #f0f0f0',
      padding: 24,
      marginBottom: 16,
    }}
  >
    <SectionHeader
      icon={<DatabaseOutlined />}
      title="记忆配置"
      subtitle="自动记忆对话上下文"
    />
    <Form.Item
      label="单次抽取事实上限（memory.max_facts_per_extraction）"
      name="memoryMaxFactsPerExtraction"
      extra={`记忆抽取器每轮对话抽取并写入的最多事实条数，空 = 系统默认（${MEMORY_MAX_FACTS_DEFAULT}）`}
    >
      <Slider
        min={MEMORY_MAX_FACTS_MIN}
        max={MEMORY_MAX_FACTS_MAX}
        marks={{ [MEMORY_MAX_FACTS_MIN]: `${MEMORY_MAX_FACTS_MIN}`, [MEMORY_MAX_FACTS_MAX]: `${MEMORY_MAX_FACTS_MAX}` }}
        ariaLabelForHandle="单次抽取事实上限"
      />
    </Form.Item>
    <Form.Item
      label="抽取提示词（memory.extraction_prompt）"
      name="memoryExtractionPrompt"
      extra="记忆抽取系统提示词；留空 = 使用内置默认。自定义时建议保留 %s(userID)/%s(agentID)/%d(maxFacts) 占位符"
    >
      <TextArea rows={4} placeholder="留空使用内置默认抽取提示词" />
    </Form.Item>
    <Form.Item
      label="抽取模型（memory.extraction_model）"
      name="memoryExtractionModel"
      extra="执行记忆抽取所用的模型；留空 = 使用全局默认"
    >
      <Select allowClear placeholder="使用全局默认" showSearch optionFilterProp="children">
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
      label="记忆召回条数（memory.recall_top_k）"
      name="memoryRecallTopK"
      extra={`执行时从记忆库召回并注入上下文的最多条目数，空 = 系统默认（${RECALL_TOP_K_DEFAULT}）`}
    >
      <Slider
        min={RECALL_TOP_K_MIN}
        max={RECALL_TOP_K_MAX}
        marks={{ [RECALL_TOP_K_MIN]: `${RECALL_TOP_K_MIN}`, [RECALL_TOP_K_MAX]: `${RECALL_TOP_K_MAX}` }}
        ariaLabelForHandle="记忆召回条数"
      />
    </Form.Item>
    <Form.Item
      label="事实注入条数（memory.fact_injection_top_n）"
      name="memoryFactInjectionTopN"
      extra={`会话上下文注入的长期事实条数，空 = 系统默认（${MEMORY_FACT_INJECTION_DEFAULT}）`}
    >
      <Slider
        min={MEMORY_FACT_INJECTION_MIN}
        max={MEMORY_FACT_INJECTION_MAX}
        marks={{ [MEMORY_FACT_INJECTION_MIN]: `${MEMORY_FACT_INJECTION_MIN}`, [MEMORY_FACT_INJECTION_MAX]: `${MEMORY_FACT_INJECTION_MAX}` }}
        ariaLabelForHandle="事实注入条数"
      />
    </Form.Item>
    <Form.Item
      label="历史注入条数（memory.history_injection_top_n）"
      name="memoryHistoryInjectionTopN"
      extra={`会话上下文注入的历史消息条数，0 = 不注入历史，空 = 系统默认（${MEMORY_HISTORY_INJECTION_DEFAULT}）`}
    >
      <Slider
        min={MEMORY_HISTORY_INJECTION_MIN}
        max={MEMORY_HISTORY_INJECTION_MAX}
        marks={{ [MEMORY_HISTORY_INJECTION_MIN]: `${MEMORY_HISTORY_INJECTION_MIN}`, [MEMORY_HISTORY_INJECTION_MAX]: `${MEMORY_HISTORY_INJECTION_MAX}` }}
        ariaLabelForHandle="历史注入条数"
      />
    </Form.Item>
    <Form.Item
      label="作用域"
      name="memoryScope"
      rules={[{ required: true, message: '请选择作用域' }]}
      extra="决定记忆的存储与检索范围"
      style={{ marginBottom: 0 }}
    >
      <Select placeholder="选择作用域" options={MEMORY_SCOPE_OPTIONS} />
    </Form.Item>
  </div>
);

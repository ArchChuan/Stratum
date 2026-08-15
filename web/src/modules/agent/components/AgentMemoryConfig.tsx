import { DatabaseOutlined } from '@ant-design/icons';
import { Form, Select, Slider } from 'antd';

import {
  MEMORY_FACT_INJECTION_MAX,
  MEMORY_FACT_INJECTION_MIN,
  MEMORY_HISTORY_INJECTION_MAX,
  MEMORY_HISTORY_INJECTION_MIN,
  MEMORY_MAX_FACTS_MAX,
  MEMORY_MAX_FACTS_MIN,
  MEMORY_SCOPE_OPTIONS,
} from '@/constants';
import { SectionHeader } from '@/shared/ui';

// 记忆注入参数。值存 agents.parameters JSONB 的 memory.* dotted 键，按 agent
// 生效（memory pipeline 经 resource resolver 按 agentID 读取）。空值 = 不覆盖，
// 回落 pkg/constants 默认（memory.* 已绑定 agent 资源，无平台层）。
// recall_top_k / long_term_top_k 无运行时消费方，不渲染。
export const AgentMemoryConfig = () => (
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
      extra="记忆抽取器每轮对话抽取并写入的最多事实条数，空 = 系统默认"
    >
      <Slider
        min={MEMORY_MAX_FACTS_MIN}
        max={MEMORY_MAX_FACTS_MAX}
        marks={{ [MEMORY_MAX_FACTS_MIN]: `${MEMORY_MAX_FACTS_MIN}`, [MEMORY_MAX_FACTS_MAX]: `${MEMORY_MAX_FACTS_MAX}` }}
        ariaLabelForHandle="单次抽取事实上限"
      />
    </Form.Item>
    <Form.Item
      label="事实注入条数（memory.fact_injection_top_n）"
      name="memoryFactInjectionTopN"
      extra="会话上下文注入的长期事实条数，空 = 系统默认"
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
      extra="会话上下文注入的历史消息条数，0 = 不注入历史，空 = 系统默认"
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

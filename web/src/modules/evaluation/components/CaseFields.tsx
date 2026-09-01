import { Form, Input, InputNumber, Select } from 'antd';

import { EVALUATION_MAX_CALLS_LIMIT } from '@/constants';

export type CaseAssertionMode = 'exact' | 'contains' | 'regex' | 'judge';
export const assertionModeOptions = [
  { value: 'exact', label: '精确匹配' },
  { value: 'contains', label: '包含匹配' },
  { value: 'regex', label: '正则匹配' },
  { value: 'judge', label: 'AI 判定' },
];

// 断言方式选择：case 进入草稿时确定；judge 需要判定模型与评分标准。
export const AssertionModeField = () => (
  <Form.Item name="assertion_mode" label="断言方式" rules={[{ required: true, message: '请选择断言方式' }]}>
    <Select aria-label="断言方式" options={assertionModeOptions} />
  </Form.Item>
);

// AI 判定配置：仅 assertion_mode = judge 时展示。模型必填（运行期 judgeCase
// 依赖 JudgeSpec），评分标准可选。judge_spec 在 case 进入草稿时持久化，编辑不抹除。
export const JudgeSpecFields = () => {
  const mode = Form.useWatch('assertion_mode');
  return mode === 'judge' ? <>
    <Form.Item name="judge_model" label="判定模型" rules={[{ required: true, message: '请输入判定模型' }]}
      extra="AI 判定模型标识（如 qwen-plus、glm-4），进入草稿后不可修改。">
      <Input aria-label="判定模型" />
    </Form.Item>
    <Form.Item name="judge_rubric" label="评分标准"><Input.TextArea aria-label="评分标准" /></Form.Item>
  </> : null;
};

// ToolSpecFields 是工具序列确定性过程断言（§6.5）编辑控件：must_call /
// must_not_call / order 用 tags 输入工具名，max_calls 用受限 InputNumber。
// 与 assertion_mode 正交——任何断言方式下都可配置过程断言，进入草稿后不可修改。
export const ToolSpecFields = () => <>
  <Form.Item name="must_call" label="必调用工具"
    extra="执行链路中必须调用的工具名，缺一即判定过程未通过。">
    <Select mode="tags" aria-label="必调用工具" placeholder="输入工具名后回车" />
  </Form.Item>
  <Form.Item name="must_not_call" label="禁止调用工具"
    extra="执行链路中禁止调用的工具名，命中即判定过程未通过。">
    <Select mode="tags" aria-label="禁止调用工具" placeholder="输入工具名后回车" />
  </Form.Item>
  <Form.Item name="tool_order" label="调用顺序"
    extra="工具必须按给定顺序出现（允许中间穿插其他调用）。">
    <Select mode="tags" aria-label="调用顺序" placeholder="输入工具名后回车" />
  </Form.Item>
  <Form.Item name="max_calls" label="最大调用次数"
    extra={`工具调用总次数上限，超过即判定过程未通过（上限 ${EVALUATION_MAX_CALLS_LIMIT}）。`}>
    <InputNumber aria-label="最大调用次数" min={1} max={EVALUATION_MAX_CALLS_LIMIT} style={{ width: '100%' }} />
  </Form.Item>
</>;

// StepJudgeFields 是步骤级 LLM rubric（§6.5）编辑控件：对工具序列逐步骤
// 评分。留空时运行期回退平台默认步骤 rubric。进入草稿后不可修改。
export const StepJudgeFields = () => <Form.Item name="step_criteria" label="步骤判定标准"
  extra="步骤级评分标准（可选），留空使用平台默认 rubric。">
  <Input.TextArea aria-label="步骤判定标准" rows={3} />
</Form.Item>;

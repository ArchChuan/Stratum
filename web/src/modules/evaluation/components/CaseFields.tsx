import { Form, Input, Select } from 'antd';

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

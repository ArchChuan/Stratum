import { Collapse, Form, Input, InputNumber, type FormRule } from 'antd';

import { parseMappingText } from '../model/editor';

const mappingValidationMessage = '映射必须是合法的 JSON 对象，值必须是字符串';
const mappingRule: FormRule = {
  validator: (_rule, value: unknown) => {
    if (typeof value !== 'string' || value.trim() === '') return Promise.resolve();
    // 与契约（zod z.record(z.string()) / Go map[string]string）双重对齐，
    // 非法值类型一旦保存会让整个工作流重载失败。
    return parseMappingText(value, {}) === null
      ? Promise.reject(new Error(mappingValidationMessage))
      : Promise.resolve();
  },
};

export const WorkflowAdvancedSettings = () => <Collapse
  ghost
  items={[{
    key: 'advanced',
    label: '高级设置',
    children: <>
      <Form.Item label="最大重试次数" name={['retry', 'max_attempts']}>
        <InputNumber aria-label="最大重试次数" min={0} max={10} precision={0} />
      </Form.Item>
      <Form.Item label="退避时间（毫秒）" name={['retry', 'backoff_ms']}>
        <InputNumber min={0} precision={0} />
      </Form.Item>
      <Form.Item label="超时时间（毫秒）" name="timeout_ms">
        <InputNumber min={0} precision={0} />
      </Form.Item>
      <Form.Item label="输入映射" name="input_mapping_text" rules={[mappingRule]} extra="使用 JSON 对象描述运行输入到节点参数的映射。">
        <Input.TextArea rows={3} placeholder={'{"query":"$.task"}'} />
      </Form.Item>
      <Form.Item label="输出映射" name="output_mapping_text" rules={[mappingRule]}>
        <Input.TextArea rows={3} placeholder={'{"summary":"$.result"}'} />
      </Form.Item>
    </>,
  }]}
/>;

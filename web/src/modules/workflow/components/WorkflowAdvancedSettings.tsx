import { Collapse, Form, Input, InputNumber } from 'antd';

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
      <Form.Item label="输入映射" name="input_mapping_text" extra="使用 JSON 对象描述运行输入到节点参数的映射。">
        <Input.TextArea rows={3} placeholder={'{"query":"$.task"}'} />
      </Form.Item>
      <Form.Item label="输出映射" name="output_mapping_text">
        <Input.TextArea rows={3} placeholder={'{"summary":"$.result"}'} />
      </Form.Item>
    </>,
  }]}
/>;

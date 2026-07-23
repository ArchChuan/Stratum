import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { Button, Form, Input, Select, Space, Switch, Typography } from 'antd';

const { Text } = Typography;

const fieldTypes = [
  { value: 'short_text', label: '短文本' },
  { value: 'long_text', label: '长文本' },
  { value: 'number', label: '数字' },
  { value: 'single_select', label: '单选' },
  { value: 'multi_select', label: '多选' },
  { value: 'boolean', label: '开关' },
  { value: 'date', label: '日期' },
];

export const WorkflowInputSchemaEditor = () => {
  const form = Form.useFormInstance();
  const uniqueKeyRule = ({ getFieldValue }: { getFieldValue: (name: string) => Array<{ key?: string }> }) => ({
    validator(_: unknown, value?: string) {
      if (!value) return Promise.resolve();
      const count = (getFieldValue('fields') || []).filter((field) => field?.key === value).length;
      return count > 1 ? Promise.reject(new Error('字段标识不能重复')) : Promise.resolve();
    },
  });

  return <section className="workflow-input-editor">
    <Text strong>运行输入</Text>
    <Form.Item label="任务名称" name="task_label" rules={[{ required: true, message: '请输入任务名称' }]}>
      <Input placeholder="例如：本次研究主题" />
    </Form.Item>
    <Form.Item label="任务说明" name="task_description">
      <Input.TextArea rows={2} placeholder="运行前向用户说明需要准备什么" />
    </Form.Item>
    <Form.List name="fields">
      {(fields, { add, remove }) => <>
        {fields.map(({ key, ...field }) => <div className="workflow-input-field" key={key}>
          <Space align="start" wrap>
            <Form.Item {...field} label="字段标识" name={[field.name, 'key']} rules={[{ required: true, message: '请输入字段标识' }, uniqueKeyRule(form)]}>
              <Input placeholder="topic" />
            </Form.Item>
            <Form.Item {...field} label="显示名称" name={[field.name, 'label']} rules={[{ required: true, message: '请输入显示名称' }]}>
              <Input placeholder="研究主题" />
            </Form.Item>
            <Form.Item {...field} label="字段类型" name={[field.name, 'type']}>
              <Select aria-label="字段类型" options={fieldTypes} style={{ width: 128 }} />
            </Form.Item>
            <Form.Item {...field} label="必填" name={[field.name, 'required']} valuePropName="checked">
              <Switch />
            </Form.Item>
            <Button aria-label="删除输入字段" danger type="text" icon={<DeleteOutlined />} onClick={() => remove(field.name)} />
          </Space>
        </div>)}
        <Button icon={<PlusOutlined />} onClick={() => add({ type: 'short_text', required: false })}>添加输入字段</Button>
      </>}
    </Form.List>
  </section>;
};

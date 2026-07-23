import { Button, DatePicker, Form, Input, InputNumber, Select, Switch } from 'antd';
import type { Dayjs } from 'dayjs';

import type { WorkflowInputField, WorkflowInputSchema } from '../model/workflow';

const controlFor = (field: WorkflowInputField) => {
  switch (field.type) {
    case 'long_text': return <Input.TextArea rows={4} />;
    case 'number': return <InputNumber style={{ width: '100%' }} />;
    case 'single_select': return <Select options={field.options} />;
    case 'multi_select': return <Select mode="multiple" options={field.options} />;
    case 'boolean': return <Switch />;
    case 'date': return <DatePicker style={{ width: '100%' }} />;
    default: return <Input />;
  }
};

export const WorkflowRunForm = ({ schema, loading, onSubmit }: {
  schema: WorkflowInputSchema;
  loading: boolean;
  onSubmit: (values: { task: string; fields: Record<string, unknown> }) => void;
}) => {
  const [form] = Form.useForm();
  const initialFields = Object.fromEntries(schema.fields.filter((field) => field.default !== undefined).map((field) => [field.key, field.default]));
  return <Form
    form={form}
    layout="vertical"
    initialValues={{ fields: initialFields }}
    onFinish={(values) => {
      const fields = Object.fromEntries(Object.entries(values.fields || {}).map(([key, value]) => [key, (value as Dayjs)?.format ? (value as Dayjs).format('YYYY-MM-DD') : value]));
      onSubmit({ task: values.task, fields });
    }}
  >
    <Form.Item label={schema.task_label} name="task" extra={schema.task_description} rules={[{ required: true, message: `请输入${schema.task_label}` }]}>
      <Input.TextArea rows={4} autoFocus />
    </Form.Item>
    {schema.fields.map((field) => <Form.Item
      key={field.key}
      label={field.label}
      name={['fields', field.key]}
      extra={field.description}
      valuePropName={field.type === 'boolean' ? 'checked' : 'value'}
      rules={[{ required: field.required, message: `请填写${field.label}` }]}
    >{controlFor(field)}</Form.Item>)}
    <Button aria-label="开始运行" type="primary" htmlType="submit" loading={loading} disabled={loading}>开始运行</Button>
  </Form>;
};

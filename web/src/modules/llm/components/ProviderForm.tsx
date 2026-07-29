import { Form, Input, Select, Modal } from 'antd';

import type { CreateProviderInput, ProviderKind } from '../model/llm';

const KIND_OPTIONS: { label: string; value: ProviderKind }[] = [
  { label: 'OpenAI 兼容', value: 'openai_compat' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Ollama', value: 'ollama' },
];

interface Props {
  open: boolean;
  onCancel: () => void;
  onSubmit: (values: CreateProviderInput) => Promise<void>;
  loading?: boolean;
}

export function ProviderForm({ open, onCancel, onSubmit, loading }: Props) {
  const [form] = Form.useForm<CreateProviderInput>();

  const handleFinish = async (values: CreateProviderInput) => {
    await onSubmit(values);
    form.resetFields();
  };

  return (
    <Modal
      title="添加厂商"
      open={open}
      onCancel={() => {
        form.resetFields();
        onCancel();
      }}
      onOk={() => form.submit()}
      confirmLoading={loading}
      destroyOnClose
    >
      <Form form={form} layout="vertical" onFinish={handleFinish}>
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入厂商名称' }]}>
          <Input placeholder="例如：我的千问" />
        </Form.Item>
        <Form.Item name="kind" label="类型" rules={[{ required: true, message: '请选择厂商类型' }]}>
          <Select options={KIND_OPTIONS} />
        </Form.Item>
        <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true, message: '请输入 Base URL' }]}>
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item name="apiKey" label="API Key" rules={[{ required: true, message: '请输入 API Key' }]}>
          <Input.Password placeholder="sk-..." />
        </Form.Item>
      </Form>
    </Modal>
  );
}

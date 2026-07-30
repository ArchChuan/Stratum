import { Form, Input, Select, Modal } from 'antd';

import type { Provider, ProviderKind } from '../model/llm';

const KIND_OPTIONS: { label: string; value: ProviderKind }[] = [
  { label: 'OpenAI 兼容', value: 'openai_compat' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Ollama', value: 'ollama' },
];

export interface ProviderFormValues {
  name: string;
  kind: ProviderKind;
  baseUrl: string;
  apiKey: string;
  defaultModel: string;
}

interface Props {
  open: boolean;
  onCancel: () => void;
  onSubmit: (values: ProviderFormValues) => Promise<void>;
  loading?: boolean;
  /** Provider to edit; omit for create mode. */
  provider?: Provider | null;
}

export function ProviderForm({ open, onCancel, onSubmit, loading, provider }: Props) {
  const [form] = Form.useForm<ProviderFormValues>();
  const isEdit = !!provider;

  const handleFinish = async (values: ProviderFormValues) => {
    await onSubmit(values);
    form.resetFields();
  };

  const handleCancel = () => {
    form.resetFields();
    onCancel();
  };

  return (
    <Modal
      title={isEdit ? '编辑厂商' : '添加厂商'}
      open={open}
      onCancel={handleCancel}
      onOk={() => form.submit()}
      confirmLoading={loading}
      destroyOnClose
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={
          provider
            ? {
                name: provider.name,
                kind: provider.kind,
                baseUrl: provider.baseUrl,
                defaultModel: provider.defaultModel,
              }
            : undefined
        }
      >
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入厂商名称' }]}>
          <Input placeholder="例如：我的千问" />
        </Form.Item>
        <Form.Item name="kind" label="类型" rules={[{ required: true, message: '请选择厂商类型' }]}>
          <Select options={KIND_OPTIONS} />
        </Form.Item>
        <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true, message: '请输入 Base URL' }]}>
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item
          name="apiKey"
          label="API Key"
          rules={isEdit ? [] : [{ required: true, message: '请输入 API Key' }]}
          extra={isEdit ? '留空则不修改已有的 API Key' : undefined}
        >
          <Input.Password placeholder={isEdit ? '留空则不修改' : 'sk-...'} />
        </Form.Item>
        <Form.Item name="defaultModel" label="默认模型" extra="可选，指定该厂商的默认模型名称">
          <Input placeholder="例如：gpt-4o" />
        </Form.Item>
      </Form>
    </Modal>
  );
}

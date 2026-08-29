import { Form, Input, InputNumber, Modal, Select, Typography } from 'antd';

import type { ModelCapability, Provider } from '../model/llm';

// 手动添加模型的可选能力清单；与模型目录筛选/编辑保持一致，不含 rerank。
const CAPABILITY_OPTIONS: { label: string; value: ModelCapability }[] = [
  { label: '对话', value: 'chat' },
  { label: '嵌入', value: 'embedding' },
  { label: '视觉', value: 'vision' },
  { label: '工具调用', value: 'tool_use' },
  { label: '推理', value: 'reasoning' },
];

export interface AddModelFormValues {
  name: string;
  capabilities: ModelCapability[];
  /** 0 表示未设置，回退到厂商默认。 */
  contextWindow: number;
  /** 0 表示未设置，回退到厂商默认。 */
  maxTokens: number;
}

interface Props {
  open: boolean;
  /** 目标厂商；空时不渲染表单内容（Modal 仅在选中厂商后打开）。 */
  provider: Provider | null;
  onCancel: () => void;
  onSubmit: (values: AddModelFormValues) => Promise<void>;
  loading?: boolean;
}

export function AddModelModal({ open, provider, onCancel, onSubmit, loading }: Props) {
  const [form] = Form.useForm<AddModelFormValues>();

  const handleFinish = async (values: AddModelFormValues) => {
    await onSubmit(values);
    form.resetFields();
  };

  const handleCancel = () => {
    form.resetFields();
    onCancel();
  };

  return (
    <Modal
      title="添加模型"
      open={open}
      onCancel={handleCancel}
      onOk={() => form.submit()}
      confirmLoading={loading}
      okText="添加"
      cancelText="取消"
      destroyOnClose
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleFinish}
        initialValues={{ contextWindow: 0, maxTokens: 0 }}
      >
        <Form.Item label="厂商">
          <Typography.Text>{provider?.name ?? '-'}</Typography.Text>
        </Form.Item>
        <Form.Item
          name="name"
          label="模型名称"
          rules={[{ required: true, whitespace: true, message: '请输入模型名称' }]}
          extra="与厂商 API 返回的模型标识一致，例如 gpt-4o"
        >
          <Input placeholder="例如：gpt-4o" />
        </Form.Item>
        <Form.Item
          name="capabilities"
          label="能力"
          rules={[{ required: true, message: '请至少选择一项能力' }]}
        >
          <Select mode="multiple" placeholder="选择模型能力" options={CAPABILITY_OPTIONS} />
        </Form.Item>
        <div style={{ display: 'flex', gap: 16 }}>
          <Form.Item
            name="contextWindow"
            label="上下文窗口"
            style={{ flex: 1 }}
            tooltip="留空或 0 表示使用厂商默认值"
          >
            <InputNumber min={0} placeholder="默认" style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item
            name="maxTokens"
            label="最大输出"
            style={{ flex: 1 }}
            tooltip="留空或 0 表示使用厂商默认值"
          >
            <InputNumber min={0} placeholder="默认" style={{ width: '100%' }} />
          </Form.Item>
        </div>
      </Form>
    </Modal>
  );
}

import { Button, Drawer, Form, Input, InputNumber, Select, Space, Switch } from 'antd';
import { useEffect } from 'react';

import type { Model, ModelCapability, UpdateModelInput } from '../model/llm';

const CAP_OPTIONS: { label: string; value: ModelCapability }[] = [
  { label: '对话', value: 'chat' },
  { label: '嵌入', value: 'embedding' },
  { label: '视觉', value: 'vision' },
  { label: '工具调用', value: 'tool_use' },
  { label: '推理', value: 'reasoning' },
];

interface Props {
  open: boolean;
  model: Model | null;
  onClose: () => void;
  onSubmit: (id: string, values: UpdateModelInput) => Promise<void>;
  loading?: boolean;
}

export function ModelEditDrawer({ open, model, onClose, onSubmit, loading }: Props) {
  const [form] = Form.useForm<UpdateModelInput>();

  useEffect(() => {
    if (model) {
      form.setFieldsValue({
        displayName: model.displayName,
        capabilities: model.capabilities,
        contextWindow: model.contextWindow,
        maxTokens: model.maxTokens,
        inputPrice: model.inputPrice,
        outputPrice: model.outputPrice,
        recommended: model.recommended,
      });
    }
  }, [model, form]);

  const handleClose = () => {
    form.resetFields();
    onClose();
  };

  return (
    <Drawer
      title={model ? `编辑模型 — ${model.displayName || model.name}` : '编辑模型'}
      open={open}
      onClose={handleClose}
      width={480}
      footer={
        <Space style={{ float: 'right' }}>
          <Button onClick={handleClose}>取消</Button>
          <Button type="primary" loading={loading} onClick={() => form.submit()}>
            保存
          </Button>
        </Space>
      }
    >
      <Form form={form} layout="vertical" onFinish={(v) => model && onSubmit(model.id, v)}>
        <Form.Item name="displayName" label="显示名称">
          <Input placeholder={model?.name || ''} />
        </Form.Item>
        <Form.Item name="capabilities" label="能力标签">
          <Select mode="multiple" options={CAP_OPTIONS} placeholder="选择能力" />
        </Form.Item>
        <Form.Item name="contextWindow" label="上下文窗口 (tokens)">
          <InputNumber min={0} style={{ width: '100%' }} placeholder="0" />
        </Form.Item>
        <Form.Item name="maxTokens" label="最大输出 (tokens)">
          <InputNumber min={0} style={{ width: '100%' }} placeholder="0" />
        </Form.Item>
        <Form.Item name="inputPrice" label="输入价格 ($/1M tokens)">
          <InputNumber min={0} step={0.01} style={{ width: '100%' }} placeholder="0" />
        </Form.Item>
        <Form.Item name="outputPrice" label="输出价格 ($/1M tokens)">
          <InputNumber min={0} step={0.01} style={{ width: '100%' }} placeholder="0" />
        </Form.Item>
        <Form.Item name="recommended" label="推荐" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Drawer>
  );
}

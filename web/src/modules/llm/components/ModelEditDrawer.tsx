import { Alert, Button, Descriptions, Divider, Drawer, Form, Input, InputNumber, Select, Space, Switch } from 'antd';
import { useEffect } from 'react';

import type { Model, ModelCapability, UpdateModelInput, UpdateModelPolicyInput } from '../model/llm';

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
  onSubmit: (id: string, values: UpdateModelInput, policy: UpdateModelPolicyInput) => Promise<void>;
  loading?: boolean;
}

type EditValues = UpdateModelInput & UpdateModelPolicyInput;

export function ModelEditDrawer({ open, model, onClose, onSubmit, loading }: Props) {
  const [form] = Form.useForm<EditValues>();

  useEffect(() => {
    if (model) {
      form.setFieldsValue({
        displayName: model.displayName,
        capabilities: model.capabilities,
        inputPrice: model.inputPrice,
        outputPrice: model.outputPrice,
        recommended: model.recommended,
        operatorContextWindow: model.operatorContextWindow,
        operatorMaxTokens: model.operatorMaxTokens,
        defaultOutputTokens: model.defaultOutputTokens,
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
      <Form form={form} layout="vertical" onFinish={(v) => model && onSubmit(model.id, {
        displayName: v.displayName,
        capabilities: v.capabilities,
        contextWindow: model.contextWindow,
        maxTokens: model.maxTokens,
        inputPrice: v.inputPrice,
        outputPrice: v.outputPrice,
        recommended: v.recommended,
      }, {
        operatorContextWindow: v.operatorContextWindow,
        operatorMaxTokens: v.operatorMaxTokens,
        defaultOutputTokens: v.defaultOutputTokens,
      })}>
        <Form.Item name="displayName" label="显示名称">
          <Input placeholder={model?.name || ''} />
        </Form.Item>
        <Form.Item name="capabilities" label="能力标签">
          <Select mode="multiple" options={CAP_OPTIONS} placeholder="选择能力" />
        </Form.Item>
        <Descriptions size="small" column={1} bordered>
          <Descriptions.Item label="观测上下文窗口">
            {model?.contextWindow?.toLocaleString() || '未知'} ({model?.contextWindowSource || 'legacy_unknown'})
          </Descriptions.Item>
          <Descriptions.Item label="观测最大输出">
            {model?.maxTokens?.toLocaleString() || '未知'} ({model?.maxTokensSource || 'legacy_unknown'})
          </Descriptions.Item>
        </Descriptions>
        <Divider orientation="left">运行策略</Divider>
        <Alert
          type="info"
          showIcon
          message="这些值只能收紧已知模型能力；留空表示沿用观测能力。"
          style={{ marginBottom: 16 }}
        />
        <Button
          size="small"
          style={{ marginBottom: 16 }}
          onClick={() => form.setFieldsValue({
            operatorContextWindow: null,
            operatorMaxTokens: null,
            defaultOutputTokens: null,
          })}
        >
          清除全部策略覆盖
        </Button>
        <Form.Item name="operatorContextWindow" label="运营上下文上限 (tokens)">
          <InputNumber min={1} style={{ width: '100%' }} placeholder="未覆盖" />
        </Form.Item>
        <Form.Item name="operatorMaxTokens" label="运营最大输出上限 (tokens)">
          <InputNumber min={1} style={{ width: '100%' }} placeholder="未覆盖" />
        </Form.Item>
        <Form.Item name="defaultOutputTokens" label="默认输出预算 (tokens)">
          <InputNumber min={1} style={{ width: '100%' }} placeholder="协议默认" />
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

import { Alert, Form, Input, Modal, Switch } from 'antd';
import { useEffect, useState } from 'react';

import type { EvaluationCase } from '../model/evaluation';

import { AssertionModeField, type CaseAssertionMode } from './CaseFields';

export interface EditDraftCaseValues {
  name: string; input: unknown; expectedOutput: unknown; assertionMode: CaseAssertionMode; enabled: boolean;
}

const toEditable = (value: unknown) => (typeof value === 'string' ? value : JSON.stringify(value ?? ''));

export const EditDraftCaseModal = ({ open, draft, onClose, onSubmit }: {
  open: boolean; draft: EvaluationCase | null; onClose: () => void;
  onSubmit: (values: EditDraftCaseValues) => Promise<void>;
}) => {
  const [form] = Form.useForm<{
    name: string; input: string; expected_output: string; assertion_mode: CaseAssertionMode; enabled: boolean;
  }>();
  const [loading, setLoading] = useState(false);
  const mode = Form.useWatch('assertion_mode', form);

  useEffect(() => {
    if (!open || !draft) return;
    form.setFieldsValue({
      name: draft.name ?? '',
      input: toEditable(draft.input),
      expected_output: toEditable(draft.expected_output),
      assertion_mode: draft.assertion_mode,
      enabled: draft.enabled ?? true,
    });
  }, [open, draft, form]);

  const close = () => { form.resetFields(); onClose(); };
  const submit = async () => {
    const values = await form.validateFields();
    setLoading(true);
    try {
      await onSubmit({
        name: values.name, input: values.input, expectedOutput: values.expected_output,
        assertionMode: values.assertion_mode, enabled: values.enabled,
      });
      close();
    } catch { /* Parent owns the persistent Chinese error notification; keep the form open. */ }
    finally { setLoading(false); }
  };
  return <Modal title="编辑草稿用例" open={open} onCancel={close} onOk={() => void submit()}
    okText="保存" cancelText="取消" confirmLoading={loading} destroyOnHidden>
    <Form form={form} layout="vertical">
      <Form.Item name="name" label="用例名称" rules={[{ required: true, message: '请输入用例名称' }]}><Input aria-label="用例名称" /></Form.Item>
      <Form.Item name="input" label="测试输入" rules={[{ required: true, message: '请输入测试输入' }]}><Input.TextArea aria-label="测试输入" /></Form.Item>
      <Form.Item name="expected_output" label="期望输出" rules={[{ required: true, message: '请输入期望输出' }]}><Input.TextArea aria-label="期望输出" /></Form.Item>
      <AssertionModeField />
      {mode === 'judge' && <Alert type="info" showIcon style={{ marginBottom: 16 }}
        message="AI 判定的模型与评分标准在 case 进入草稿时确定，编辑不可修改。" />}
      <Form.Item name="enabled" label="包含在本版本" valuePropName="checked">
        <Switch aria-label="包含在本版本" />
      </Form.Item>
    </Form>
  </Modal>;
};

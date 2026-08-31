import { Form, Input, Modal, Select, Switch } from 'antd';
import { useState } from 'react';

import type { ResourceKind } from '../model/evaluation';

import { AssertionModeField, JudgeSpecFields, type CaseAssertionMode } from './CaseFields';
import { displayLabel } from './evaluationView';

const resourceOptions = ['skill', 'agent', 'mcp', 'knowledge'].map((value) => ({ value, label: displayLabel(value) }));

export interface CreateSuiteValues {
  resource_kind: ResourceKind; name: string; description?: string;
  case_name: string; input: string; expected_output: string;
  assertion_mode: CaseAssertionMode; judge_model?: string; judge_rubric?: string; enabled: boolean;
}

export const CreateSuiteModal = ({ open, onClose, onSubmit }: {
  open: boolean; onClose: () => void; onSubmit: (values: CreateSuiteValues) => Promise<void>;
}) => {
  const [form] = Form.useForm<CreateSuiteValues>();
  const [loading, setLoading] = useState(false);
  const close = () => { form.resetFields(); onClose(); };
  const submit = async () => {
    const values = await form.validateFields();
    setLoading(true);
    try { await onSubmit(values); close(); }
    catch { /* Parent owns the persistent Chinese error notification; keep the form open. */ }
    finally { setLoading(false); }
  };
  return <Modal title="新建套件" open={open} onCancel={close} onOk={() => void submit()}
    okText="创建" cancelText="取消" confirmLoading={loading} destroyOnHidden>
    <Form form={form} layout="vertical" initialValues={{ assertion_mode: 'contains', enabled: true }}>
      <Form.Item name="resource_kind" label="资源类型" rules={[{ required: true, message: '请选择资源类型' }]}>
        <Select aria-label="资源类型" options={resourceOptions} />
      </Form.Item>
      <Form.Item name="name" label="套件名称" rules={[{ required: true, message: '请输入套件名称' }]}><Input aria-label="套件名称" /></Form.Item>
      <Form.Item name="description" label="套件说明"><Input aria-label="套件说明" /></Form.Item>
      <Form.Item name="case_name" label="用例名称" rules={[{ required: true, message: '请输入用例名称' }]}><Input aria-label="用例名称" /></Form.Item>
      <Form.Item name="input" label="测试输入" rules={[{ required: true, message: '请输入测试输入' }]}><Input.TextArea aria-label="测试输入" /></Form.Item>
      <Form.Item name="expected_output" label="期望输出" rules={[{ required: true, message: '请输入期望输出' }]}><Input.TextArea aria-label="期望输出" /></Form.Item>
      <AssertionModeField />
      <JudgeSpecFields />
      <Form.Item name="enabled" label="包含在本版本" valuePropName="checked">
        <Switch aria-label="包含在本版本" />
      </Form.Item>
    </Form>
  </Modal>;
};

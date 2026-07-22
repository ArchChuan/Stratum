import { Form, Input, Modal, Select } from 'antd';
import { useState } from 'react';

import type { ResourceSummary } from '../model/evaluation';

interface Values { resource_id: string; name: string; description?: string; case_name: string; input: string; expected_output: string }

export const CreateEvaluationModal = ({ open, resources, onClose, onSubmit }: {
  open: boolean; resources: ResourceSummary[]; onClose: () => void;
  onSubmit: (values: Values, resource: ResourceSummary) => Promise<void>;
}) => {
  const [form] = Form.useForm<Values>();
  const [loading, setLoading] = useState(false);
  const submit = async () => {
    const values = await form.validateFields();
    const resource = resources.find((item) => item.id === values.resource_id);
    if (!resource) return;
    setLoading(true);
    try { await onSubmit(values, resource); form.resetFields(); onClose(); }
    catch { /* Parent owns the persistent Chinese error notification; keep the form open. */ }
    finally { setLoading(false); }
  };
  return <Modal title="新建评测" open={open} onCancel={onClose} onOk={() => void submit()} okText="创建并运行"
    cancelText="取消" confirmLoading={loading} destroyOnHidden>
    <Form form={form} layout="vertical">
      <Form.Item name="resource_id" label="目标资源" rules={[{ required: true, message: '请选择目标资源' }]}>
        <Select aria-label="目标资源" options={resources.filter((item) => item.stable_revision_id)
          .map((item) => ({ value: item.id, label: `${String(item.safe_summary.name || item.resource_id)}（${item.resource_id}）` }))} />
      </Form.Item>
      <Form.Item name="name" label="评测名称" rules={[{ required: true, message: '请输入评测名称' }]}><Input aria-label="评测名称" /></Form.Item>
      <Form.Item name="description" label="评测说明"><Input aria-label="评测说明" /></Form.Item>
      <Form.Item name="case_name" label="用例名称" rules={[{ required: true, message: '请输入用例名称' }]}><Input aria-label="用例名称" /></Form.Item>
      <Form.Item name="input" label="测试输入" rules={[{ required: true, message: '请输入测试输入' }]}><Input aria-label="测试输入" /></Form.Item>
      <Form.Item name="expected_output" label="期望输出" rules={[{ required: true, message: '请输入期望输出' }]}><Input aria-label="期望输出" /></Form.Item>
    </Form>
  </Modal>;
};

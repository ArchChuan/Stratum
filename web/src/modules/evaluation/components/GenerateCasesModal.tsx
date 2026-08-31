import { Form, InputNumber, Modal, Select } from 'antd';
import { useState } from 'react';

import { EVALUATION_GENERATE_DEFAULT_MAX_CASES, EVALUATION_GENERATE_MAX_CASES } from '@/constants';

export type SamplePolicy = 'negative_first' | 'balanced';

export interface GenerateCasesValues { samplePolicy: SamplePolicy; maxCases?: number }

export const GenerateCasesModal = ({ open, onClose, onSubmit }: {
  open: boolean; onClose: () => void; onSubmit: (values: GenerateCasesValues) => Promise<void>;
}) => {
  const [form] = Form.useForm<GenerateCasesValues>();
  const [loading, setLoading] = useState(false);
  const close = () => { form.resetFields(); onClose(); };
  const submit = async () => {
    const values = await form.validateFields();
    setLoading(true);
    try { await onSubmit(values); close(); }
    catch { /* Parent owns the persistent Chinese error notification; keep the form open. */ }
    finally { setLoading(false); }
  };
  return <Modal title="从生产采样生成草稿用例" open={open} onCancel={close} onOk={() => void submit()}
    okText="生成" cancelText="取消" confirmLoading={loading} destroyOnHidden>
    <Form form={form} layout="vertical"
      initialValues={{ samplePolicy: 'balanced', maxCases: EVALUATION_GENERATE_DEFAULT_MAX_CASES }}>
      <Form.Item name="samplePolicy" label="采样策略" rules={[{ required: true, message: '请选择采样策略' }]}
        extra="balanced 正反样本均衡；negative_first 优先负样本（失败与未达预期）。">
        <Select aria-label="采样策略" options={[
          { value: 'balanced', label: '均衡采样' },
          { value: 'negative_first', label: '负样本优先' },
        ]} />
      </Form.Item>
      <Form.Item name="maxCases" label="最大用例数" rules={[{ required: true, message: '请输入最大用例数' }]}>
        <InputNumber aria-label="最大用例数" min={1} max={EVALUATION_GENERATE_MAX_CASES} style={{ width: '100%' }} />
      </Form.Item>
    </Form>
  </Modal>;
};

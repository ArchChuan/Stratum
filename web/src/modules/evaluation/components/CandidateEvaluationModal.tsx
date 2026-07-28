import { Form, Input, Modal } from 'antd';
import { useRef, useState } from 'react';

import { createIdempotencyKey } from '@/shared/lib/idempotencyKey';

interface Values { suiteRevisionId: string }

export const CandidateEvaluationModal = ({ open, onClose, onSubmit }: {
  open: boolean; onClose: () => void;
  onSubmit: (suiteRevisionId: string, idempotencyKey: string) => Promise<void>;
}) => {
  const [form] = Form.useForm<Values>();
  const [loading, setLoading] = useState(false);
  const idempotencyKey = useRef(createIdempotencyKey());
  const close = () => {
    idempotencyKey.current = createIdempotencyKey();
    form.resetFields();
    onClose();
  };
  const submit = async () => {
    const { suiteRevisionId } = await form.validateFields();
    setLoading(true);
    try {
      await onSubmit(suiteRevisionId, idempotencyKey.current);
      close();
    } catch {
      // The page owns the persistent error notification; keep the form open for correction or retry.
    } finally {
      setLoading(false);
    }
  };
  return <Modal title="运行候选离线评测" open={open} onCancel={close} onOk={() => void submit()}
    okText="开始评测" cancelText="取消" confirmLoading={loading} destroyOnHidden>
    <Form form={form} layout="vertical">
      <Form.Item name="suiteRevisionId" label="Suite Revision ID"
        rules={[{ required: true, message: '请输入 Suite Revision ID' }]}
        extra="候选必须通过此评测套件后才能创建金丝雀实验。">
        <Input aria-label="Suite Revision ID" />
      </Form.Item>
    </Form>
  </Modal>;
};

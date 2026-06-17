import { Button, Form, Input, Modal, Select } from 'antd';
import { useEffect } from 'react';

interface Props {
  open: boolean;
  loading: boolean;
  onCancel: () => void;
  onSubmit: (values: { email: string; role: 'admin' | 'member' }) => void;
}

export const TenantInviteModal = ({ open, loading, onCancel, onSubmit }: Props) => {
  const [form] = Form.useForm<{ email: string; role: 'admin' | 'member' }>();

  useEffect(() => {
    if (!open) form.resetFields();
  }, [open, form]);

  return (
    <Modal title="邀请成员" open={open} onCancel={onCancel} footer={null} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={onSubmit} style={{ marginTop: 16 }}>
        <Form.Item
          label="邮箱"
          name="email"
          rules={[{ required: true, type: 'email', message: '请输入有效邮箱' }]}
        >
          <Input placeholder="例如：user@example.com" />
        </Form.Item>
        <Form.Item label="角色" name="role" initialValue="member">
          <Select
            options={[
              { value: 'member', label: '普通成员' },
              { value: 'admin', label: '管理员' },
            ]}
          />
        </Form.Item>
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" block loading={loading}>
            发送邀请
          </Button>
        </Form.Item>
      </Form>
    </Modal>
  );
};

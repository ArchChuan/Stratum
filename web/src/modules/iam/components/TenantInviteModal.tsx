import { Alert, Button, Form, Input, Modal, Select, Space, Typography } from 'antd';
import { useEffect } from 'react';

interface Props {
  open: boolean;
  loading: boolean;
  invitationCode: string;
  onCancel: () => void;
  onSubmit: (values: { email: string; role: 'admin' | 'member' }) => void;
}

export const TenantInviteModal = ({ open, loading, invitationCode, onCancel, onSubmit }: Props) => {
  const [form] = Form.useForm<{ email: string; role: 'admin' | 'member' }>();

  useEffect(() => {
    if (!open) form.resetFields();
  }, [open, form]);

  return (
    <Modal
      title={invitationCode ? '邀请码已生成' : '邀请成员'}
      open={open}
      onCancel={onCancel}
      footer={null}
      destroyOnHidden
    >
      {invitationCode ? (
        <Space direction="vertical" size={16} style={{ width: '100%', marginTop: 16 }}>
          <Alert
            type="warning"
            showIcon
            message="请立即复制邀请码"
            description="关闭窗口后将不再显示。请通过可信渠道发送给受邀成员。"
          />
          <Typography.Text
            code
            copyable={{ text: invitationCode, tooltips: ['复制邀请码', '已复制'] }}
            style={{ display: 'block', overflowWrap: 'anywhere', padding: 12 }}
          >
            {invitationCode}
          </Typography.Text>
          <Button type="primary" block onClick={onCancel}>
            完成
          </Button>
        </Space>
      ) : (
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
      )}
    </Modal>
  );
};
